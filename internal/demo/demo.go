// Package demo serves the public /demo/ surface — a zero-token,
// in-memory-only conformance demo of the M6.3 sponsored_context
// envelope + sponsored_context_receipt flow. Designed for the WG-SI
// audience: someone clicks a link, chats with the brand agent in a
// browser, and watches the envelope + receipt land on the wire on
// every turn without needing curl, Python, or Claude Desktop.
//
// Three design constraints:
//
//   1. **No persistence.** Sessions live in process memory only.
//      A deploy restart wipes every demo session. Audit endpoint
//      returns the in-memory trail; no SQLite touched, no PII at rest.
//
//   2. **BYOK with mock fallback.** The catalog and the wire envelope
//      work even with zero LLM credit — bragent's offline mock
//      responder is the default. Demo viewers who want a real
//      conversation paste their own Anthropic / OpenAI / Groq key
//      via the UI; the key flows only on that request, never logged,
//      never stored. An explicit endpoint whitelist (we are not
//      a free SSRF proxy) guards which providers BYOK can target.
//
//   3. **Catalog is read-only.** /demo/api/products lists the same
//      fixture the rest of bragent serves; there is no create/delete
//      route. The admin path stays the only mutating surface.
//
// Rate limiting is per-IP, 30 requests/hour, in-memory token-bucket.
// Demo viewers running real chat through Anthropic can burn ~30 turns
// per hour before getting throttled — enough to show the flow.
package demo

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kapoost/bragent/internal/config"
	"github.com/kapoost/bragent/internal/feed"
	"github.com/kapoost/bragent/internal/llm"
)

//go:embed static/*
var staticFS embed.FS

// allowedLLMEndpoints is the closed list of OpenAI-compatible base URLs
// the demo BYOK proxy will forward to. Keeps bragent from being abused
// as a free SSRF surface that can hit arbitrary internal hosts. Adding
// a provider here is a deliberate review step.
var allowedLLMEndpoints = []string{
	"https://api.anthropic.com/",
	"https://api.openai.com/",
	"https://api.groq.com/",
	"https://api.together.xyz/",
	"https://api.deepseek.com/",
	"https://api.mistral.ai/",
}

// Handler is the public demo multiplexer.
type Handler struct {
	cfg       *config.Config
	catalog   *feed.Catalog
	static    fs.FS
	defaultLM llm.Provider

	mu       sync.Mutex
	sessions map[string]*demoSession

	limiter *limiter
}

func New(cfg *config.Config, catalog *feed.Catalog) *Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("demo: embedded static missing: " + err.Error())
	}
	return &Handler{
		cfg:       cfg,
		catalog:   catalog,
		static:    sub,
		defaultLM: llm.NewMock(),
		sessions:  map[string]*demoSession{},
		limiter:   newLimiter(30, time.Hour),
	}
}

// demoSession holds an in-memory chat. Cleared on deploy. The wire
// envelope is recomputed on every turn from the same config the rest
// of bragent uses, so the demo's responses match what the public /mcp
// endpoint would emit verbatim.
type demoSession struct {
	ID               string
	CreatedAt        time.Time
	Turns            []demoTurn
	LastSponsoredCtx map[string]any // last envelope we emitted
	Receipts         []demoReceipt  // synthesised + persisted in-memory
	OfferingID       string
	Intent           string
}

type demoTurn struct {
	Turn      int
	Role      string // host | brand
	Content   string
	CreatedAt time.Time
}

type demoReceipt struct {
	Turn        int    // brand turn the receipt acknowledges
	Status      string // accepted | rejected
	AcceptedUse string
	ReceivedAt  time.Time
	Synthesised bool
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/demo")
	if path == "" || path == "/" {
		h.serveStatic(w, r, "index.html")
		return
	}
	// Apply rate limit on the API routes only — static assets shouldn't
	// count against the budget (the page is one HTML + one JS load).
	if strings.HasPrefix(path, "/api/") {
		if !h.limiter.allow(remoteIP(r)) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit: 30 demo requests/hour/IP"})
			return
		}
	}
	switch {
	case path == "/api/products" && r.Method == http.MethodGet:
		h.listProducts(w, r)
	case path == "/api/chat" && r.Method == http.MethodPost:
		h.chat(w, r)
	case strings.HasPrefix(path, "/api/sessions/") && strings.HasSuffix(path, "/audit") && r.Method == http.MethodGet:
		sid := strings.TrimSuffix(strings.TrimPrefix(path, "/api/sessions/"), "/audit")
		h.audit(w, r, sid)
	case strings.HasPrefix(path, "/"):
		h.serveStatic(w, r, strings.TrimPrefix(path, "/"))
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) serveStatic(w http.ResponseWriter, r *http.Request, name string) {
	b, err := fs.ReadFile(h.static, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Write(b)
}

func (h *Handler) listProducts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"brand":    h.cfg.Brand.Name,
		"domain":   h.cfg.Brand.Domain,
		"products": h.catalog.All(),
	})
}

type chatRequest struct {
	SessionID  string    `json:"session_id,omitempty"`
	Message    string    `json:"message"`
	Intent     string    `json:"intent,omitempty"`
	OfferingID string    `json:"offering_id,omitempty"`
	LLM        *byokSpec `json:"llm,omitempty"`
}

// byokSpec carries the user's BYOK credentials. Endpoint MUST match the
// allowedLLMEndpoints prefix list — any other value falls back to the
// mock provider rather than risking an SSRF.
type byokSpec struct {
	Endpoint string `json:"endpoint,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	Model    string `json:"model,omitempty"`
}

type chatResponse struct {
	SessionID        string         `json:"session_id"`
	SessionStatus    string         `json:"session_status"`
	Response         map[string]any `json:"response"`
	Handoff          map[string]any `json:"handoff,omitempty"`
	SponsoredContext map[string]any `json:"sponsored_context"`
	PriorReceipt     map[string]any `json:"prior_receipt,omitempty"`
	LLMProvider      string         `json:"llm_provider"`
}

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message required"})
		return
	}

	provider, providerName := h.resolveProvider(req.LLM)

	h.mu.Lock()
	defer h.mu.Unlock()

	var sess *demoSession
	if req.SessionID != "" {
		sess = h.sessions[req.SessionID]
	}
	priorReceipt := map[string]any(nil)
	if sess == nil {
		// New session: initiate. Welcome turn at turn 0, host's first
		// message as turn 1.
		sid := newID("dsess")
		sess = &demoSession{
			ID:         sid,
			CreatedAt:  time.Now().UTC(),
			OfferingID: req.OfferingID,
			Intent:     firstNonEmpty(req.Intent, req.Message),
		}
		welcome := buildWelcome(h.cfg.Brand.Name, req.OfferingID, req.Intent)
		sess.Turns = append(sess.Turns, demoTurn{Turn: 0, Role: "brand", Content: welcome, CreatedAt: time.Now().UTC()})
		sess.LastSponsoredCtx = buildSponsoredContext(h.cfg, "presentation_only")
		h.sessions[sid] = sess
	} else {
		// Existing session — synthesise an accepted receipt for the
		// prior brand turn so the demo flow shows the dual-trail.
		if sess.LastSponsoredCtx != nil {
			r := synthReceipt(sess.LastSponsoredCtx, true) // accept-presentation default
			brandTurn := lastBrandTurn(sess.Turns)
			sess.Receipts = append(sess.Receipts, demoReceipt{
				Turn:        brandTurn,
				Status:      "accepted",
				AcceptedUse: "presentation_only",
				ReceivedAt:  time.Now().UTC(),
				Synthesised: true,
			})
			priorReceipt = r
		}
	}

	// Append the host turn.
	hostTurn := len(sess.Turns)
	sess.Turns = append(sess.Turns, demoTurn{Turn: hostTurn, Role: "host", Content: req.Message, CreatedAt: time.Now().UTC()})

	// Build history for the LLM.
	turns := make([]llm.Turn, 0, len(sess.Turns))
	for _, t := range sess.Turns {
		turns = append(turns, llm.Turn{Role: t.Role, Content: t.Content})
	}

	reply := provider.Reply(llm.ReplyRequest{
		BrandName:   h.cfg.Brand.Name,
		BrandDomain: h.cfg.Brand.Domain,
		OfferingID:  sess.OfferingID,
		UserText:    req.Message,
		History:     turns,
		Catalog:     h.catalog.All(),
	})

	brandTurn := len(sess.Turns)
	sess.Turns = append(sess.Turns, demoTurn{Turn: brandTurn, Role: "brand", Content: reply.Message, CreatedAt: time.Now().UTC()})
	sess.LastSponsoredCtx = buildSponsoredContext(h.cfg, "presentation_only")

	resp := chatResponse{
		SessionID:        sess.ID,
		SessionStatus:    reply.SessionStatus,
		Response:         map[string]any{"message": reply.Message},
		SponsoredContext: sess.LastSponsoredCtx,
		PriorReceipt:     priorReceipt,
		LLMProvider:      providerName,
	}
	if reply.SessionStatus == "" {
		resp.SessionStatus = "active"
	}
	if reply.HandoffURL != "" {
		resp.Handoff = map[string]any{"url": reply.HandoffURL, "session_id": sess.ID}
	}
	writeJSON(w, http.StatusOK, resp)
}

// resolveProvider picks the LLM provider for this turn. BYOK takes
// priority when endpoint is whitelisted and key is non-empty; otherwise
// the offline Mock answers. The provider name returned drives the UI
// indicator so testers always know which backend produced the reply.
func (h *Handler) resolveProvider(byok *byokSpec) (llm.Provider, string) {
	if byok == nil || byok.APIKey == "" || byok.Endpoint == "" {
		return h.defaultLM, "mock"
	}
	if !endpointAllowed(byok.Endpoint) {
		return h.defaultLM, "mock (BYOK endpoint not in whitelist)"
	}
	return llm.NewOpenAI(byok.Endpoint, byok.APIKey, byok.Model), "byok:" + byok.Endpoint
}

func endpointAllowed(url string) bool {
	for _, p := range allowedLLMEndpoints {
		if strings.HasPrefix(url, p) {
			return true
		}
	}
	return false
}

func (h *Handler) audit(w http.ResponseWriter, r *http.Request, sid string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sess, ok := h.sessions[sid]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found (in-memory only, may have been cleared on deploy)"})
		return
	}
	rcptByTurn := map[int]demoReceipt{}
	for _, r := range sess.Receipts {
		rcptByTurn[r.Turn] = r
	}
	turns := make([]map[string]any, 0, len(sess.Turns))
	for _, t := range sess.Turns {
		row := map[string]any{
			"turn":       t.Turn,
			"role":       t.Role,
			"content":    t.Content,
			"created_at": t.CreatedAt.Format(time.RFC3339),
		}
		if t.Role == "brand" {
			if r, ok := rcptByTurn[t.Turn]; ok {
				row["receipt"] = map[string]any{
					"status":       r.Status,
					"accepted_use": r.AcceptedUse,
					"synthesised":  r.Synthesised,
					"received_at":  r.ReceivedAt.Format(time.RFC3339),
				}
			}
		}
		turns = append(turns, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":         sess.ID,
		"brand_name":         h.cfg.Brand.Name,
		"brand_domain":       h.cfg.Brand.Domain,
		"declared_context":   "presentation_only",
		"intent":             sess.Intent,
		"offering_id":        sess.OfferingID,
		"created_at":         sess.CreatedAt.Format(time.RFC3339),
		"turns":              turns,
		"sponsored_context":  sess.LastSponsoredCtx,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func remoteIP(r *http.Request) string {
	// Trust X-Forwarded-For when behind Fly's edge (Fly sets it). Take
	// the first hop — the original client IP. Falls back to RemoteAddr
	// when not proxied.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := splitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// splitHostPort handles IPv6 brackets that net.SplitHostPort would
// require — small wrapper to keep the call sites clean.
func splitHostPort(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "", "", errors.New("no port")
	}
	return addr[:i], addr[i+1:], nil
}

// limiter is a tiny in-memory IP token bucket. capacity tokens refill
// over window. Cheap, lockable, eviction handled lazily on access.
type limiter struct {
	mu       sync.Mutex
	capacity int
	window   time.Duration
	buckets  map[string]*bucket
}

type bucket struct {
	tokens    int
	resetAt   time.Time
	lastTouch time.Time
}

func newLimiter(cap int, window time.Duration) *limiter {
	return &limiter{capacity: cap, window: window, buckets: map[string]*bucket{}}
}

func (l *limiter) allow(ip string) bool {
	if ip == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok || now.After(b.resetAt) {
		b = &bucket{tokens: l.capacity, resetAt: now.Add(l.window)}
		l.buckets[ip] = b
	}
	b.lastTouch = now
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	// Lazy eviction: every 100 accesses, sweep stale buckets so a
	// long-running process doesn't accumulate a per-IP entry forever.
	if len(l.buckets) > 100 && len(l.buckets)%50 == 0 {
		for k, v := range l.buckets {
			if now.Sub(v.lastTouch) > 2*l.window {
				delete(l.buckets, k)
			}
		}
	}
	return true
}

// buildSponsoredContext mirrors the si.handlers buildSponsoredContext
// shape — we duplicate it here on purpose so /demo/ doesn't import the
// si package (would couple two unrelated transports). The two builders
// stay in sync via the shared config struct.
func buildSponsoredContext(cfg *config.Config, contextUse string) map[string]any {
	juris := []map[string]any{}
	for _, j := range cfg.Brand.Disclosure.Jurisdictions {
		juris = append(juris, map[string]any{
			"country":    j.Country,
			"region":     j.Region,
			"regulation": j.Regulation,
		})
	}
	return map[string]any{
		"paying_principal": map[string]any{
			"brand": map[string]any{
				"domain": cfg.Brand.Domain,
			},
			"display_name": cfg.Brand.Name,
		},
		"context_use": contextUse,
		"disclosure_obligation": map[string]any{
			"required":      cfg.Brand.Disclosure.Required,
			"label_text":    cfg.Brand.Disclosure.LabelText,
			"timing":        cfg.Brand.Disclosure.Timing,
			"proximity":     cfg.Brand.Disclosure.Proximity,
			"jurisdictions": juris,
		},
		"declared_at": time.Now().UTC().Format(time.RFC3339),
		"declared_by": map[string]any{
			"agent_url": "https://" + cfg.Brand.Domain + "/mcp",
			"role":      "brand_agent",
		},
	}
}

// synthReceipt builds an accepted receipt for accept-presentation mode.
// The demo always operates in that mode (presentation_only declared,
// presentation_only accepted) so the dual-trail visualisation is
// pedagogically clean — viewers see "declared X → accepted X → no
// mismatch" and grok the flow before having to think about reject paths.
func synthReceipt(sponsored map[string]any, accept bool) map[string]any {
	use, _ := sponsored["context_use"].(string)
	receipt := map[string]any{
		"sponsored_context": sponsored,
		"host_receipt": map[string]any{
			"status":       map[bool]string{true: "accepted", false: "rejected"}[accept],
			"received_at":  time.Now().UTC().Format(time.RFC3339),
			"host_surface": "demo-panel-synthesized",
		},
	}
	if accept {
		hr := receipt["host_receipt"].(map[string]any)
		hr["accepted_context_use"] = use
		hr["disclosure_commitment"] = map[string]any{"status": "not_required"}
	}
	return receipt
}

func buildWelcome(brandName, offeringID, intent string) string {
	if offeringID != "" {
		return "Hi from " + brandName + ". You're looking at our " + offeringID + " — I can help you with details, alternatives, or get you to checkout. What would you like to know?"
	}
	if intent != "" {
		return "Hi from " + brandName + ". You're interested in: " + intent + " — happy to dig in. What's the most important thing for you?"
	}
	return "Hi from " + brandName + ". I'm the brand assistant — ask me anything about our products or pricing."
}

func newID(prefix string) string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func lastBrandTurn(turns []demoTurn) int {
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "brand" {
			return turns[i].Turn
		}
	}
	return 0
}
