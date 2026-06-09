package si

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/kapoost/bragent/internal/config"
	"github.com/kapoost/bragent/internal/feed"
	"github.com/kapoost/bragent/internal/mcp"
)

type Handlers struct {
	cfg     *config.Config
	catalog *feed.Catalog
}

func NewHandlers(cfg *config.Config, catalog *feed.Catalog) *Handlers {
	return &Handlers{cfg: cfg, catalog: catalog}
}

func (h *Handlers) Handle(ctx context.Context, method string, params json.RawMessage) (any, *mcp.Error) {
	switch method {
	case "get_adcp_capabilities":
		return h.capabilities(), nil
	case "si_get_offering":
		return h.getOffering(ctx, params)
	case "si_initiate_session", "si_send_message", "si_terminate_session":
		return nil, &mcp.Error{
			Code:    mcp.ErrMethodNotFound,
			Message: fmt.Sprintf("method %s recognised but not implemented in M1 (spec TBD as of 2026-06-09)", method),
		}
	default:
		return nil, &mcp.Error{Code: mcp.ErrMethodNotFound, Message: "unknown method: " + method}
	}
}

func (h *Handlers) capabilities() CapabilitiesResponse {
	return CapabilitiesResponse{
		AdCPVersion:        "3.0",
		Role:               "brand",
		Specialisms:        []string{"sponsored_intelligence.core"},
		SupportedProtocols: []string{"sponsored_intelligence"},
		Capabilities: []string{
			"get_adcp_capabilities",
			"si_get_offering",
		},
		AgentName: h.cfg.Brand.Name,
		AgentURL:  fmt.Sprintf("https://%s/mcp", h.cfg.Brand.Domain),
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
			OfferingID:  p.ID,
			Title:       p.Name,
			Description: p.Description,
			Price:       p.Price,
			Currency:    p.Currency,
			URL:         p.URL,
			Available:   p.Available,
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
	}, nil
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "offering_" + hex.EncodeToString(b), nil
}
