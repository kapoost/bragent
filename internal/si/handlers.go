package si

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kapoost/bragent/internal/brand"
	"github.com/kapoost/bragent/internal/config"
	"github.com/kapoost/bragent/internal/feed"
	"github.com/kapoost/bragent/internal/llm"
	"github.com/kapoost/bragent/internal/mcp"
	"github.com/kapoost/bragent/internal/signing"
	"github.com/kapoost/bragent/internal/store"
)

type Handlers struct {
	cfg     *config.Config
	catalog *feed.Catalog
	store   *store.Store
	llm     llm.Provider
	brand   *brand.Handler  // optional — wired when [brand].signing_key_path is set
	signer  *signing.Signer // optional — same key as brand; used to notarise receipts (M6.3)
}

func NewHandlers(cfg *config.Config, catalog *feed.Catalog, st *store.Store, provider llm.Provider) *Handlers {
	return &Handlers{cfg: cfg, catalog: catalog, store: st, llm: provider}
}

// WithSigner attaches the receipt-notarisation signer. Same Ed25519 key
// used by M6.1 verify_brand_claim; one trust story per brand identity.
// Optional — without it, receipts are stored unsigned and admin audit
// flags them as un-notarised.
func (h *Handlers) WithSigner(s *signing.Signer) *Handlers {
	h.signer = s
	return h
}

// WithBrand attaches the brand-protocol surface (M6.1). Off when nil —
// capabilities omits verify_brand_claim and the dispatcher returns
// method_not_found, matching the spec's "tool absent → unsupported".
func (h *Handlers) WithBrand(b *brand.Handler) *Handlers {
	h.brand = b
	return h
}

func (h *Handlers) Handle(ctx context.Context, method string, params json.RawMessage) (any, *mcp.Error) {
	var inner any
	var rpcErr *mcp.Error
	switch method {
	case "get_adcp_capabilities":
		inner = h.capabilities()
	case "si_get_offering":
		inner, rpcErr = h.getOffering(ctx, params)
	case "si_initiate_session":
		inner, rpcErr = h.initiateSession(ctx, params)
	case "si_send_message":
		inner, rpcErr = h.sendMessage(ctx, params)
	case "si_terminate_session":
		inner, rpcErr = h.terminateSession(ctx, params)
	case "verify_brand_claim":
		if h.brand == nil {
			return nil, &mcp.Error{Code: mcp.ErrMethodNotFound, Message: "verify_brand_claim not configured"}
		}
		inner, rpcErr = h.brand.VerifyBrandClaim(ctx, params)
	default:
		return nil, &mcp.Error{Code: mcp.ErrMethodNotFound, Message: "unknown method: " + method}
	}
	if rpcErr != nil {
		return nil, rpcErr
	}
	return wrapEnvelope(inner, params), nil
}

// wrapEnvelope injects the AdCP 3.1.0-rc.* v3 envelope fields onto every
// outgoing response. Per /schemas/3.1.0-rc.14/core/protocol-envelope.json:
//
//   - status (REQUIRED) — task-status enum value; sync metadata calls use
//     "completed". A response without this field fails
//     v3_envelope_integrity/envelope_integrity_check even when the body
//     schema would otherwise validate.
//   - context (REQUIRED for echo) — byte-for-byte echo of the caller's
//     `context` object. Used for trace IDs, correlation, UI session
//     stitching. The version_negotiation and v3_envelope_integrity
//     storyboards both assert context.correlation_id round-trips.
//   - adcp_version (advisory at 3.1, MUST at 4.0) — release-precision
//     string of the AdCP release the server actually served. We declare
//     supported_versions ["3.0", "3.1-rc"]; echo "3.1-rc" because the
//     envelope shape we now emit IS the 3.1.0-rc.* shape.
//
// The wrap flattens the body fields to siblings of the envelope fields
// per MCP/REST transport rules ("envelope fields and task-body fields
// are siblings at the root of the tool response" — protocol-envelope.json
// notes). On A2A the same envelope fields map to task metadata; we
// don't speak A2A yet, so the MCP/REST shape is the only one.
func wrapEnvelope(inner any, params json.RawMessage) any {
	out := map[string]any{
		"status":       "completed",
		"adcp_version": "3.1-rc",
	}
	// Echo per-request `context` byte-for-byte. AAO comply runner sends
	// `{context: {correlation_id: "<storyboard>--<step>"}}` and asserts
	// the echo round-trips with the same correlation_id value.
	if len(params) > 0 {
		var req struct {
			Context json.RawMessage `json:"context"`
		}
		if err := json.Unmarshal(params, &req); err == nil && len(req.Context) > 0 {
			out["context"] = json.RawMessage(req.Context)
		}
	}
	// Flatten the inner response onto the envelope. Per protocol-envelope.json
	// notes: "On MCP the body fields appear as siblings of envelope fields
	// at the root of the tool response". The envelope wins on field-name
	// collision (the body has no reason to declare `status` etc).
	body, err := json.Marshal(inner)
	if err != nil {
		// Marshal failure on a typed handler return is a real bug;
		// surface inner as-is in a `payload` key rather than swallow.
		out["payload"] = inner
		return out
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		out["payload"] = inner
		return out
	}
	for k, v := range bodyMap {
		if _, taken := out[k]; taken {
			continue
		}
		out[k] = v
	}
	return out
}

func (h *Handlers) capabilities() CapabilitiesResponse {
	// Canonical hyphenated ID only (`sponsored-intelligence`). M1
	// shipped the underscored `sponsored_intelligence.core` and M5a
	// added the canonical alongside as additive spec sync — but
	// downstream consumers (AAO comply runner, 3.1.0-beta.7+ caches)
	// iterate ALL specialisms and reject on any unknown ID. No public
	// host has been observed matching on the underscored form, so the
	// alias has no users to protect; drop it cleanly.
	specialisms := []string{"sponsored-intelligence"}
	tools := []string{
		"get_adcp_capabilities",
		"si_get_offering",
		"si_initiate_session",
		"si_send_message",
		"si_terminate_session",
	}
	protocols := []string{"sponsored_intelligence"}
	if h.brand != nil {
		// brand-rights is `preview` in the 3.1.x AdCPSpecialism enum;
		// we advertise it alongside the SI specialism once the signer
		// is wired so hosts that filter on brand-rights pick us up.
		specialisms = append(specialisms, "brand-rights")
		protocols = append(protocols, "brand")
		tools = append(tools, "verify_brand_claim")
	}
	return CapabilitiesResponse{
		AdCPVersion: "3.0",
		Role:        "brand",
		// Emit both the legacy underscored ID we shipped with M1
		// (`sponsored_intelligence.core`) and the spec-canonical
		// hyphenated ID (`sponsored-intelligence`) introduced in
		// 3.1.0-rc.* AdCPSpecialism enum. Hosts that match either
		// form pick us up; the duplication is harmless.
		Specialisms:        specialisms,
		SupportedProtocols: protocols,
		Capabilities:       tools,
		AgentName:          h.cfg.Brand.Name,
		AgentURL:           fmt.Sprintf("https://%s/mcp", h.cfg.Brand.Domain),
		// Version negotiation. Declare both the stable 3.0 release we
		// fully implement AND the 3.1-rc prerelease — bragent already
		// honours sponsored-intelligence/sponsored_context (3.1.0-rc.14
		// surface) for SI hosts that pin a prerelease target. Without
		// this block AAO comply runners refuse to dispatch prerelease
		// scenarios with "agent does not advertise support for that
		// target".
		AdCP: AdCPCapabilities{
			MajorVersions:     []int{3},
			SupportedVersions: []string{"3.0", "3.1-rc"},
		},
		// M6.3: capabilities goes back to being a thin discovery surface.
		// The M6.2-era PayingPrincipal + InfluenceModesSupported extension
		// fields are gone — sponsored_context now travels in every SI
		// response envelope, so hosts learn accountability per-turn
		// instead of pre-negotiating it on capabilities.
	}
}

// buildSponsoredContext composes the M6.3 sponsored_context envelope
// for a given outgoing response. ContextUse is the per-emission
// declaration — getOffering and the initial welcome are
// presentation_only by default (the wire shape suggests "render as a
// labeled card, do not fold into reasoning"); send_message responses
// where bragent has already promoted to pending_handoff stay
// presentation_only too since the handoff URL is the rendered unit.
//
// In a future revision we can let the LLM provider hint a different
// use mode (e.g., reasoning_context when the brand wants the host
// model to factor the answer into a comparison). For now, conservative
// default: presentation_only. The audit trail will show every emission.
func (h *Handlers) buildSponsoredContext(ctx ContextUse) *SponsoredContext {
	if !ctx.IsValid() {
		ctx = ContextUsePresentationOnly
	}
	dcfg := h.cfg.Brand.Disclosure
	juris := make([]DisclosureJurisdiction, 0, len(dcfg.Jurisdictions))
	for _, j := range dcfg.Jurisdictions {
		juris = append(juris, DisclosureJurisdiction{
			Country:    j.Country,
			Region:     j.Region,
			Regulation: j.Regulation,
		})
	}
	return &SponsoredContext{
		PayingPrincipal: PayingPrincipal{
			Brand:       BrandRef{Domain: h.cfg.Brand.Domain},
			DisplayName: h.cfg.Brand.Name,
		},
		ContextUse: ctx,
		DisclosureObligation: DisclosureObligation{
			Required:      dcfg.Required,
			LabelText:     dcfg.LabelText,
			Timing:        dcfg.Timing,
			Proximity:     dcfg.Proximity,
			Jurisdictions: juris,
		},
		DeclaredAt: nowRFC3339(),
		DeclaredBy: &DeclaredBy{
			AgentURL: fmt.Sprintf("https://%s/mcp", h.cfg.Brand.Domain),
			Role:     "brand_agent",
		},
	}
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

func (h *Handlers) getOffering(_ context.Context, params json.RawMessage) (any, *mcp.Error) {
	var req OfferingPreviewRequest
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: err.Error()}
		}
	}
	limit := req.MaxResults
	if limit <= 0 {
		limit = 5
	}

	products := h.catalog.Search(req.Query, limit)
	offerings := make([]Offering, 0, len(products))
	for _, p := range products {
		offerings = append(offerings, Offering{
			OfferingID:         p.ID,
			Title:              p.Name,
			Description:        p.Description,
			Price:              p.Price,
			Currency:           p.Currency,
			URL:                p.URL,
			Available:          p.Available,
			AvailabilityStatus: availabilityFromFeed(p.Available),
		})
	}

	token, err := randomToken()
	if err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: "token: " + err.Error()}
	}

	return OfferingPreviewResponse{
		Offerings:     offerings,
		OfferingToken: token,
		BrandName:     h.cfg.Brand.Name,
		BrandDomain:   h.cfg.Brand.Domain,
		// Disclaimer is appended to every preview — brand owns the catalog,
		// final price/availability are confirmed only at the brand's own
		// checkout. Hardcoded to keep host UIs from rendering stale data
		// as authoritative.
		Disclaimer: fmt.Sprintf(
			"This preview represents %s based on their published product feed. Final price and availability are confirmed only on %s.",
			h.cfg.Brand.Name, h.cfg.Brand.Domain,
		),
		// M6.3 — getOffering surfaces a sponsored_context envelope. The
		// returned offerings/matching_products are sponsored content
		// entering the host boundary; the declaration applies to the
		// package as a whole per spec rc.14 §Sponsored Context Accountability.
		SponsoredContext: h.buildSponsoredContext(ContextUsePresentationOnly),
		Context:          req.Context,
	}, nil
}

// silence linter on the time import: rand+hex are used by randomToken,
// but `time` is used inside handlers.go only via buildSponsoredContext
// → nowRFC3339. Defensive guard so future refactors don't drop the
// import without noticing.
var _ = time.RFC3339

// availabilityFromFeed maps the boolean feed flag to the spec
// availability_status enum. Conservative defaults: a present, in-stock
// product → "available"; out-of-stock → "sold_out". Brands can later
// publish richer states by extending the feed schema.
func availabilityFromFeed(available bool) AvailabilityStatus {
	if available {
		return AvailabilityAvailable
	}
	return AvailabilitySoldOut
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "offering_" + hex.EncodeToString(b), nil
}
