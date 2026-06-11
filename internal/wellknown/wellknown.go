// Package wellknown serves the AdCP discovery files at /.well-known/.
//
// `brand.json` follows the AAO Brand Protocol shape and lets buyer agents
// resolve who this brand is without crawling the catalog. `adagents.json`
// declares which agents are authorized to act for the brand — this single-
// publisher brand agent declares itself and (optionally) its hosted seller.
//
// Both files are static for the lifetime of the process and rendered from
// the config struct on each request; no template engine, no caching layer.
// Buyer-side crawlers typically pull these once per discovery cycle so the
// runtime cost is negligible.
package wellknown

import (
	"encoding/json"
	"net/http"

	"github.com/kapoost/bragent/internal/config"
	"github.com/kapoost/bragent/internal/signing"
)

type Handler struct {
	cfg    *config.Config
	signer *signing.Signer // optional; only populated when brand-rights signing is wired
}

func New(cfg *config.Config) *Handler { return &Handler{cfg: cfg} }

// WithSigner attaches a response-signing key so /.well-known/jwks.json
// can publish its public half. Required for verify_brand_claim verifiers.
func (h *Handler) WithSigner(s *signing.Signer) *Handler {
	h.signer = s
	return h
}

// JWKSJSON returns the brand agent's published JWK Set. Always returns
// a (possibly empty) keys[] array — verifiers reading an empty set
// learn definitively that this agent has no response-signing key,
// rather than guessing from a 404.
func (h *Handler) JWKSJSON() map[string]any {
	keys := []any{}
	if h.signer != nil {
		keys = append(keys, h.signer.JWK())
	}
	return map[string]any{"keys": keys}
}

// BrandJSON returns the AAO Brand Protocol manifest. Minimal fields — we
// leave editorial-rich data (logo, taglines, social) to the operator's
// own brand.json on the brand's primary domain.
func (h *Handler) BrandJSON() map[string]any {
	out := map[string]any{
		"$schema":     "https://agenticadvertising.org/schemas/v1/brand.json",
		"brand_name":  h.cfg.Brand.Name,
		"domain":      h.cfg.Brand.Domain,
		"brand_agent": "https://" + h.cfg.Brand.Domain + "/mcp",
	}
	if h.cfg.Brand.LogoURL != "" {
		out["logo_url"] = h.cfg.Brand.LogoURL
	}
	return out
}

// AdAgentsJSON returns the AdCP adagents.json manifest — a publisher-side
// authorization record. The brand agent IS the authorized agent here; if
// the brand also runs a hosted seller, that goes in the same list.
func (h *Handler) AdAgentsJSON() map[string]any {
	agentURL := "https://" + h.cfg.Brand.Domain + "/mcp"
	return map[string]any{
		"$schema":     "https://adcontextprotocol.org/schemas/v3/adagents.json",
		"last_updated": "2026-06-10",
		"authorized_agents": []map[string]any{
			{
				"url":               agentURL,
				"agent_url":         agentURL,
				"authorized_for":    "Sponsored Intelligence brand agent for " + h.cfg.Brand.Name,
				"authorization_type": "domain",
				"delegation_type":   "direct",
			},
		},
	}
}

// ServeHTTP routes the well-known endpoints. Anything else returns 404
// so the MCP transport handler can take over for /mcp et al.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/brand.json":
		writeJSON(w, h.BrandJSON())
	case "/.well-known/adagents.json":
		writeJSON(w, h.AdAgentsJSON())
	case "/.well-known/jwks.json":
		writeJSON(w, h.JWKSJSON())
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(v)
}
