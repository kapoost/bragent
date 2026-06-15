package si

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/kapoost/bragent/internal/mcp"
	"github.com/kapoost/bragent/internal/store"
)

// initiateSession opens a multi-turn conversation. M6.3 wires three
// new responsibilities on top of the M2/M3 lifecycle:
//
//  1. The session emits a sponsored_context envelope on the welcome
//     turn (every brand-agent response is sponsored content).
//  2. If the host carries a sponsored_context_receipt for the
//     pre-session si_get_offering it acknowledged, we validate it
//     against the spec's allOf constraints, notarise the JSON, and
//     persist it with turn = -1 (the "pre-session" sentinel).
//  3. The session row keeps an `influence_mode` column (DB artifact
//     name) populated with the ContextUse we emitted — so the audit
//     trail joins cleanly across turns.
func (h *Handlers) initiateSession(ctx context.Context, params json.RawMessage) (any, *mcp.Error) {
	var req InitiateSessionRequest
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: err.Error()}
		}
	}

	if h.store == nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: "session store not configured"}
	}

	// Validate the optional incoming receipt before allocating a
	// session — a malformed receipt should not leave a half-baked
	// session row behind.
	if req.SponsoredContextReceipt != nil {
		if err := req.SponsoredContextReceipt.Validate(); err != nil {
			return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: "sponsored_context_receipt: " + err.Error()}
		}
	}

	sessionID, err := randomID("sess")
	if err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: "session id: " + err.Error()}
	}

	identityJSON, err := store.MarshalIdentity(req.Identity)
	if err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: "identity marshal: " + err.Error()}
	}
	capabilitiesJSON, err := store.MarshalIdentity(req.SupportedCapabilities)
	if err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: "capabilities marshal: " + err.Error()}
	}

	consent := req.Identity != nil && req.Identity.ConsentGranted

	// M6.3 — the brand-agent always emits presentation_only on the
	// welcome turn. The richer use modes (comparison_set, reasoning_
	// context) can be opted into per send_message in a future revision
	// where the LLM provider hints the mode for a given answer.
	emittedUse := ContextUsePresentationOnly

	sess := store.Session{
		SessionID:      sessionID,
		SessionStatus:  "active",
		MediaBuyID:     req.MediaBuyID,
		OfferingID:     req.OfferingID,
		Placement:      req.Placement,
		Locale:         req.Locale,
		Intent:         req.Intent,
		ConsentGranted: consent,
		Identity:       identityJSON,
		Capabilities:   capabilitiesJSON,
		InfluenceMode:  string(emittedUse),
	}
	if err := h.store.CreateSession(ctx, sess); err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: err.Error()}
	}

	welcome := h.welcomeMessage(req)
	if err := h.store.AppendMessage(ctx, store.Message{
		SessionID: sessionID,
		Turn:      0,
		Role:      "brand",
		Content:   welcome,
	}); err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: err.Error()}
	}

	// Persist the host's pre-session receipt (if present) AFTER the
	// session row exists so the FK constraint holds. Turn = -1 is the
	// sentinel reserved for receipts that acknowledge a si_get_offering
	// that happened before the session was opened.
	if req.SponsoredContextReceipt != nil {
		if err := h.persistReceipt(ctx, sessionID, -1, req.SponsoredContextReceipt); err != nil {
			// Receipt persistence failures are logged but not fatal — the
			// session is up and the spec doesn't require receipt storage
			// to be a hard gate. Operators see the failure in stderr.
			log.Printf("si: persist pre-session receipt failed: %v", err)
		}
	}

	return InitiateSessionResponse{
		SessionID:     sessionID,
		SessionStatus: "active",
		Response: SessionTurnResponse{
			Message: welcome,
		},
		BrandName:        h.cfg.Brand.Name,
		BrandDomain:      h.cfg.Brand.Domain,
		SponsoredContext: h.buildSponsoredContext(emittedUse),
		Context:          req.Context,
	}, nil
}

// persistReceipt validates → notarises (when a signer is wired) →
// writes the receipt row. Idempotent on (session_id, turn).
func (h *Handlers) persistReceipt(ctx context.Context, sessionID string, turn int, r *SponsoredContextReceipt) error {
	if r == nil {
		return nil
	}
	rawJSON, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal receipt: %w", err)
	}
	receivedAt := time.Now().UTC()
	row := store.Receipt{
		SessionID:   sessionID,
		Turn:        turn,
		RawJSON:     string(rawJSON),
		ReceivedAt:  receivedAt,
		Accepted:    r.HostReceipt.Status == "accepted",
		AcceptedUse: string(r.HostReceipt.AcceptedContextUse),
		// MCP bridges mark their synthesised receipts with
		// host_surface="bridge-synthesized". We can't modify the
		// embedded sponsored_context (that would invalidate the
		// brand's declaration), so the host_surface field is the
		// canonical synth marker per AdCP envelope semantics.
		Synthesised: r.HostReceipt.HostSurface == "bridge-synthesized",
	}
	if h.signer != nil {
		hash := sha256.Sum256(rawJSON)
		notaryPayload := map[string]any{
			"typ":            "adcp-bragent-receipt-notary+jws",
			"session_id":     sessionID,
			"turn":           turn,
			"receipt_sha256": hex.EncodeToString(hash[:]),
			"received_at":    receivedAt.Format(time.RFC3339Nano),
			"signer_kid":     h.signer.KeyID(),
		}
		jws, err := h.signer.SignReceiptNotary(notaryPayload)
		if err != nil {
			return fmt.Errorf("notarise receipt: %w", err)
		}
		row.NotaryJWS = jws
	}
	return h.store.SaveReceipt(ctx, row)
}

func (h *Handlers) welcomeMessage(req InitiateSessionRequest) string {
	if req.OfferingID != "" {
		return fmt.Sprintf(
			"Hi from %s. You're looking at our %s — I can help you with details, alternatives, or get you to checkout. What would you like to know?",
			h.cfg.Brand.Name, req.OfferingID,
		)
	}
	if req.Intent != "" {
		return fmt.Sprintf(
			"Hi from %s. You're interested in: %q — happy to dig in. What's the most important thing for you?",
			h.cfg.Brand.Name, req.Intent,
		)
	}
	return fmt.Sprintf(
		"Hi from %s. I'm the brand assistant — ask me anything about our products or pricing.",
		h.cfg.Brand.Name,
	)
}

func randomID(prefix string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b), nil
}
