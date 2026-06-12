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
	"strings"

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
//
// paying_principal is M6.2's economic disclosure primitive: the URL of
// the party economically responsible for this agent's inference. Buyer
// agents and end-user surfaces can render "paid for by <principal>"
// without needing to crawl billing or invoice records. Mapped to FTC
// "material connection" doctrine and EU DSA Art. 26.
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
	if h.cfg.Brand.PayingPrincipal != "" {
		out["paying_principal"] = h.cfg.Brand.PayingPrincipal
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

// ServeHTTP routes the well-known endpoints + the root landing page.
// Anything else returns 404 so the MCP transport handler can take over
// for /mcp et al. (the mux puts more-specific routes first).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write([]byte(h.IndexHTML()))
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

// IndexHTML renders the public landing page served at /. Single-page,
// no JS, no external assets — buyer agents don't crawl HTML but humans
// who get this URL from a chat or a search result deserve to understand
// what they're looking at. The page surfaces the paying_principal
// disclosure (M6.2) at the top so the "who pays for this agent's
// inference" answer is the first thing a visitor sees.
func (h *Handler) IndexHTML() string {
	brandName := htmlEscape(h.cfg.Brand.Name)
	brandDomain := htmlEscape(h.cfg.Brand.Domain)
	principal := htmlEscape(h.cfg.Brand.PayingPrincipal)
	mcpURL := "https://" + brandDomain + "/mcp"
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>` + brandName + ` — AdCP brand agent</title>
<style>
:root { --bg:#0f1115; --panel:#1a1d24; --fg:#e5e7eb; --fg-dim:#9ca3af; --accent:#60a5fa; --accent-2:#34d399; --border:#2d323d; --mono:ui-monospace,"JetBrains Mono",Menlo,monospace; }
* { box-sizing:border-box; }
body { margin:0; font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif; background:var(--bg); color:var(--fg); line-height:1.55; }
.wrap { max-width:760px; margin:0 auto; padding:48px 24px; }
h1 { font-size:22px; font-weight:600; margin:0 0 4px; }
h2 { font-size:13px; text-transform:uppercase; letter-spacing:0.06em; color:var(--fg-dim); margin:32px 0 8px; font-weight:600; }
.sub { color:var(--fg-dim); font-size:14px; margin:0 0 24px; }
.badge { display:inline-flex; gap:8px; align-items:center; background:var(--panel); border:1px solid var(--accent-2); color:var(--accent-2); padding:8px 12px; border-radius:6px; font-size:13px; margin-bottom:8px; }
.badge code { font-family:var(--mono); font-size:12px; color:var(--fg); }
ul.surfaces { list-style:none; padding:0; margin:0; }
ul.surfaces li { background:var(--panel); border:1px solid var(--border); border-radius:6px; padding:12px 14px; margin-bottom:8px; }
ul.surfaces a { color:var(--accent); text-decoration:none; font-family:var(--mono); font-size:13px; }
ul.surfaces a:hover { text-decoration:underline; }
ul.surfaces .desc { color:var(--fg-dim); font-size:12px; margin-top:4px; }
pre { background:var(--panel); border:1px solid var(--border); border-radius:6px; padding:12px 14px; overflow-x:auto; font-family:var(--mono); font-size:12px; color:var(--fg); }
.foot { color:var(--fg-dim); font-size:12px; margin-top:40px; padding-top:16px; border-top:1px solid var(--border); }
.foot a { color:var(--fg-dim); }
</style>
</head>
<body>
<div class="wrap">
  <h1>` + brandName + `</h1>
  <p class="sub">This server is an <a href="https://docs.adcontextprotocol.org/docs/sponsored-intelligence" style="color:var(--accent)">AdCP Sponsored Intelligence</a> brand agent. It is intended to be called by AI assistants and buyer agents over the Sponsored Intelligence protocol — not browsed by humans directly.</p>

  <div class="badge">
    Paying principal: <code>` + principal + `</code>
  </div>
  <p class="sub" style="font-size:12px; margin-top:4px">Economic disclosure: the entity above pays for this agent's inference. Maps to FTC material-connection doctrine and EU DSA Art. 26.</p>

  <h2>Public surfaces</h2>
  <ul class="surfaces">
    <li>
      <a href="/.well-known/brand.json">/.well-known/brand.json</a>
      <div class="desc">AAO Brand Protocol manifest — brand identity + paying principal</div>
    </li>
    <li>
      <a href="/.well-known/adagents.json">/.well-known/adagents.json</a>
      <div class="desc">AdCP adagents.json — authorized agent declaration</div>
    </li>
    <li>
      <a href="/.well-known/jwks.json">/.well-known/jwks.json</a>
      <div class="desc">JWK Set — public key for verify_brand_claim signed responses (M6.1)</div>
    </li>
    <li>
      <a href="/.well-known/healthz">/.well-known/healthz</a>
      <div class="desc">Liveness probe</div>
    </li>
    <li>
      <code style="color:var(--accent)">POST /mcp</code>
      <div class="desc">MCP / JSON-RPC 2.0 endpoint — all SI methods (capabilities, get_offering, initiate/send/terminate session)</div>
    </li>
  </ul>

  <h2>Quick capabilities probe</h2>
  <pre>curl -sX POST ` + mcpURL + ` \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"get_adcp_capabilities"}' | jq</pre>

  <div class="foot">
    Powered by <a href="https://github.com/kapoost/bragent">bragent</a> — open-source single-binary AdCP SI brand agent · Apache 2.0
  </div>
</div>
</body>
</html>
`
}

// htmlEscape is a tiny minimal escaper for the four characters that
// could break the inline HTML template. Brand fields come from operator-
// controlled TOML so the threat model is "stupid not malicious", but
// escaping costs nothing.
func htmlEscape(s string) string {
	r := s
	r = strings.ReplaceAll(r, "&", "&amp;")
	r = strings.ReplaceAll(r, "<", "&lt;")
	r = strings.ReplaceAll(r, ">", "&gt;")
	r = strings.ReplaceAll(r, `"`, "&quot;")
	return r
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(v)
}
