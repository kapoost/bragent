package si

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/kapoost/bragent/internal/mcp"
	"github.com/kapoost/bragent/internal/store"
)

// initiateSession opens a multi-turn conversation between the host's user
// and this brand agent. The first turn is a deterministic welcome message —
// the model-generated turns happen in si_send_message (M3 territory). M2
// proves the lifecycle: persistent session row, message log, session_id
// that subsequent calls can pin to.
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

	// M6.2 — negotiate influence_mode. Empty → default presentation_only.
	// Unknown → reject (silent downgrade would defeat the audit-trail
	// purpose). The agreed mode is persisted on the session row so every
	// subsequent send_message turn can echo it from a single source of
	// truth.
	mode := req.InfluenceMode
	if mode == "" {
		mode = InfluenceModePresentationOnly
	}
	if !mode.IsValid() {
		return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: "unknown influence_mode: " + string(mode)}
	}

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
		InfluenceMode:  string(mode),
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

	return InitiateSessionResponse{
		SessionID:     sessionID,
		SessionStatus: "active",
		Response: SessionTurnResponse{
			Message: welcome,
		},
		BrandName:       h.cfg.Brand.Name,
		BrandDomain:     h.cfg.Brand.Domain,
		PayingPrincipal: h.cfg.Brand.PayingPrincipal,
		InfluenceMode:   mode,
		Context:         req.Context,
	}, nil
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
