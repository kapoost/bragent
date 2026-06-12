package si

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/kapoost/bragent/internal/brand"
	"github.com/kapoost/bragent/internal/config"
	"github.com/kapoost/bragent/internal/feed"
	"github.com/kapoost/bragent/internal/llm"
	"github.com/kapoost/bragent/internal/mcp"
	"github.com/kapoost/bragent/internal/store"
)

type Handlers struct {
	cfg     *config.Config
	catalog *feed.Catalog
	store   *store.Store
	llm     llm.Provider
	brand   *brand.Handler // optional — wired when [brand].signing_key_path is set
}

func NewHandlers(cfg *config.Config, catalog *feed.Catalog, st *store.Store, provider llm.Provider) *Handlers {
	return &Handlers{cfg: cfg, catalog: catalog, store: st, llm: provider}
}

// WithBrand attaches the brand-protocol surface (M6.1). Off when nil —
// capabilities omits verify_brand_claim and the dispatcher returns
// method_not_found, matching the spec's "tool absent → unsupported".
func (h *Handlers) WithBrand(b *brand.Handler) *Handlers {
	h.brand = b
	return h
}

func (h *Handlers) Handle(ctx context.Context, method string, params json.RawMessage) (any, *mcp.Error) {
	switch method {
	case "get_adcp_capabilities":
		return h.capabilities(), nil
	case "si_get_offering":
		return h.getOffering(ctx, params)
	case "si_initiate_session":
		return h.initiateSession(ctx, params)
	case "si_send_message":
		return h.sendMessage(ctx, params)
	case "si_terminate_session":
		return h.terminateSession(ctx, params)
	case "verify_brand_claim":
		if h.brand == nil {
			return nil, &mcp.Error{Code: mcp.ErrMethodNotFound, Message: "verify_brand_claim not configured"}
		}
		return h.brand.VerifyBrandClaim(ctx, params)
	default:
		return nil, &mcp.Error{Code: mcp.ErrMethodNotFound, Message: "unknown method: " + method}
	}
}

func (h *Handlers) capabilities() CapabilitiesResponse {
	specialisms := []string{"sponsored_intelligence.core", "sponsored-intelligence"}
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
		// M6.2 — economic disclosure + influence-mode advertise. Hosts
		// that don't know the field ignore it; hosts that do can render
		// a "paid for by X" trust badge from the capabilities response
		// alone, without a second /.well-known/brand.json fetch.
		PayingPrincipal:         h.cfg.Brand.PayingPrincipal,
		InfluenceModesSupported: SupportedInfluenceModes,
	}
}

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
		Context: req.Context,
	}, nil
}

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
