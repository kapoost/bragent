// Package admin serves the optional /admin/ surface: a tiny embedded
// web UI for catalog CRUD plus an in-process chat panel that drives
// the brand agent's own SI handlers (no host loopback). Off by default;
// activated via [admin] enabled + token in TOML.
//
// All /admin/* routes (HTML, JS, JSON API) require X-Admin-Token. The
// public well-knowns and /mcp stay untouched — admin is bolted on as
// a sibling subtree, not a wrapper.
package admin

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"github.com/kapoost/bragent/internal/config"
	"github.com/kapoost/bragent/internal/feed"
	"github.com/kapoost/bragent/internal/mcp"
	"github.com/kapoost/bragent/internal/store"
)

//go:embed static/*
var staticFS embed.FS

// adminCookie carries the admin token for browser-initiated requests
// that can't easily set X-Admin-Token: <script src="app.js">, page
// reloads of /admin/, image fetches. Set HttpOnly + SameSite=Strict +
// Path=/admin so it's never visible to other origins or paths. Session
// cookie (no MaxAge) — clears when the browser closes.
const adminCookie = "bragent_admin_token"

// Handler is the /admin/ multiplexer. Holds direct references to the
// catalog (for CRUD), the SI dispatcher (for the chat panel), and the
// session store (for the M6.2 audit endpoint). The chat panel calls SI
// handlers in-process — same code path as the wire MCP route, no second
// HTTP hop.
type Handler struct {
	token   string
	catalog *feed.Catalog
	si      mcp.Handler
	store   *store.Store
	cfg     *config.Config
	static  fs.FS
}

func New(token string, catalog *feed.Catalog, si mcp.Handler, st *store.Store, cfg *config.Config) *Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Compile-time guaranteed by the embed directive; panic on drift
		// rather than silently boot with no UI.
		panic("admin: embedded static missing: " + err.Error())
	}
	return &Handler{token: token, catalog: catalog, si: si, store: st, cfg: cfg, static: sub}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorised(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid X-Admin-Token"})
		return
	}
	h.ensureCookie(w, r)
	path := strings.TrimPrefix(r.URL.Path, "/admin")
	if path == "" || path == "/" {
		h.serveStatic(w, r, "index.html")
		return
	}
	switch {
	case path == "/api/products" && r.Method == http.MethodGet:
		h.listProducts(w, r)
	case path == "/api/products" && r.Method == http.MethodPost:
		h.upsertProduct(w, r)
	case strings.HasPrefix(path, "/api/products/") && r.Method == http.MethodDelete:
		h.deleteProduct(w, r, strings.TrimPrefix(path, "/api/products/"))
	case path == "/api/chat" && r.Method == http.MethodPost:
		h.chat(w, r)
	case strings.HasPrefix(path, "/api/sessions/") && strings.HasSuffix(path, "/audit") && r.Method == http.MethodGet:
		// M6.2 — Masse primitive #3 surface. Returns the per-turn record
		// of an SI session: declared influence_mode, paying_principal,
		// every message in order. Admin-token gated so operators (and,
		// behind a proxy, regulators) can fetch the evidence without
		// running a SQL client.
		sid := strings.TrimSuffix(strings.TrimPrefix(path, "/api/sessions/"), "/audit")
		h.sessionAudit(w, r, sid)
	case strings.HasPrefix(path, "/"):
		h.serveStatic(w, r, strings.TrimPrefix(path, "/"))
	default:
		http.NotFound(w, r)
	}
}

// authorised accepts the token in three places, in priority order:
//
//  1. X-Admin-Token header — used by fetch() from the embedded JS for
//     all JSON API calls.
//  2. bragent_admin_token cookie — set on first successful auth so the
//     browser can fetch /admin/app.js, reload /admin/, and pull other
//     subresources without re-presenting the token.
//  3. ?token=... query string — one-time URL bootstrap. The embedded JS
//     strips it from the address bar after capture; the cookie inherits
//     the auth for everything afterward.
//
// Empty configured token always denies — silent fail-safe documented in
// config.applyDefaultsAndValidate.
func (h *Handler) authorised(r *http.Request) bool {
	if h.token == "" {
		return false
	}
	if r.Header.Get("X-Admin-Token") == h.token {
		return true
	}
	if c, err := r.Cookie(adminCookie); err == nil && c.Value == h.token {
		return true
	}
	if r.URL.Query().Get("token") == h.token {
		return true
	}
	return false
}

// ensureCookie sets the admin session cookie if the current request
// doesn't already carry it. Idempotent — repeat auth via header or
// query won't duplicate Set-Cookie headers on the wire because the
// browser already knows the value.
func (h *Handler) ensureCookie(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(adminCookie); err == nil && c.Value == h.token {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookie,
		Value:    h.token,
		Path:     "/admin",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
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
	// Defeat Safari/Chrome aggressive caching during iteration. The admin
	// UI is tiny; the cost of re-fetching is irrelevant compared to the
	// debug pain of stale JS while editing. Production users behind a
	// reverse proxy can layer CDN caching above this if they want it.
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write(b)
}

type catalogView struct {
	Brand    string         `json:"brand"`
	Writable bool           `json:"writable"`
	Products []feed.Product `json:"products"`
}

func (h *Handler) listProducts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, catalogView{
		Brand:    h.cfg.Brand.Name,
		Writable: h.catalog.Writable(),
		Products: h.catalog.All(),
	})
}

func (h *Handler) upsertProduct(w http.ResponseWriter, r *http.Request) {
	var p feed.Product
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := h.catalog.Upsert(p); err != nil {
		if errors.Is(err, feed.ErrFeedReadOnly) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) deleteProduct(w http.ResponseWriter, _ *http.Request, id string) {
	existed, err := h.catalog.Delete(id)
	if err != nil {
		if errors.Is(err, feed.ErrFeedReadOnly) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !existed {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "deleted": "true"})
}

// chat is a thin facade over the SI dispatcher. The browser holds a
// session_id in sessionStorage; first turn omits it and the server
// invokes si_initiate_session, subsequent turns invoke si_send_message.
// We deliberately do not invent a new wire shape — the JSON the admin
// chat hits is identical to the SI request shape, so the same handler
// chain runs in both contexts.
type chatRequest struct {
	SessionID string `json:"session_id,omitempty"`
	Intent    string `json:"intent,omitempty"`
	Message   string `json:"message,omitempty"`
	Offering  string `json:"offering_id,omitempty"`
}

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := r.Context()
	if req.SessionID == "" {
		intent := req.Intent
		if intent == "" {
			intent = req.Message
		}
		params, _ := json.Marshal(map[string]any{
			"intent":      intent,
			"offering_id": req.Offering,
			"locale":      "en-US",
			"identity": map[string]any{
				"consent_granted": true,
				"user_pseudo_id":  "admin-ui-001",
				"user_language":   "en",
			},
		})
		result, rpcErr := h.si.Handle(ctx, "si_initiate_session", params)
		if rpcErr != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": rpcErr.Message, "code": rpcErr.Code})
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	params, _ := json.Marshal(map[string]any{
		"session_id": req.SessionID,
		"message":    req.Message,
	})
	result, rpcErr := h.si.Handle(ctx, "si_send_message", params)
	if rpcErr != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": rpcErr.Message, "code": rpcErr.Code})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// sessionAuditView is the wire shape of GET /admin/api/sessions/:id/audit.
// M6.3 dual-trail: every brand turn ships with the sponsored_context it
// emitted (declared_context_use) plus the host's sponsored_context_
// receipt that acknowledged it (accepted_status, accepted_use,
// notary_jws). A mismatch row — accepted_use != declared_context_use,
// or accepted_status == "rejected" — is the smoking-gun the spec's
// audit-trail requirement is built around.
type sessionAuditView struct {
	SessionID           string                       `json:"session_id"`
	SessionStatus       string                       `json:"session_status"`
	BrandName           string                       `json:"brand_name"`
	BrandDomain         string                       `json:"brand_domain"`
	PayingPrincipalBrand string                      `json:"paying_principal_brand"`
	DeclaredContextUse  string                       `json:"declared_context_use"`
	OfferingID          string                       `json:"offering_id,omitempty"`
	Intent              string                       `json:"intent,omitempty"`
	ConsentGranted      bool                         `json:"consent_granted"`
	CreatedAt           string                       `json:"created_at"`
	UpdatedAt           string                       `json:"updated_at"`
	Turns               []sessionAuditTurn           `json:"turns"`
	PreSessionReceipt   *sessionAuditReceipt         `json:"pre_session_receipt,omitempty"`
}

// sessionAuditTurn folds the brand-emitted message with the host receipt
// that acknowledged it (when one exists). The optional Receipt may also
// carry a Mismatch flag the admin UI surfaces in red.
type sessionAuditTurn struct {
	Turn      int                  `json:"turn"`
	Role      string               `json:"role"`
	Content   string               `json:"content"`
	CreatedAt string               `json:"created_at"`
	Receipt   *sessionAuditReceipt `json:"receipt,omitempty"`
}

type sessionAuditReceipt struct {
	Status          string `json:"status"`              // accepted | rejected
	AcceptedUse     string `json:"accepted_use,omitempty"`
	Synthesised     bool   `json:"synthesised"`         // true when the MCP bridge stamped it
	NotaryJWS       string `json:"notary_jws,omitempty"`
	NotaryAvailable bool   `json:"notary_available"`    // false when no signing key is wired
	Mismatch        bool   `json:"mismatch,omitempty"`  // accepted use differs from declared use
	ReceivedAt      string `json:"received_at"`
	RejectionReason string `json:"rejection_reason,omitempty"`
}

func (h *Handler) sessionAudit(w http.ResponseWriter, r *http.Request, sid string) {
	if sid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id required"})
		return
	}
	ctx := r.Context()
	sess, err := h.store.GetSession(ctx, sid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found: " + sid})
		return
	}
	msgs, err := h.store.ListMessages(ctx, sid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	receipts, err := h.store.ListReceipts(ctx, sid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Index receipts by turn for O(1) lookup as we render messages.
	rcptByTurn := map[int]sessionAuditReceipt{}
	var preSession *sessionAuditReceipt
	for _, r := range receipts {
		view := sessionAuditReceipt{
			Status:          map[bool]string{true: "accepted", false: "rejected"}[r.Accepted],
			AcceptedUse:     r.AcceptedUse,
			Synthesised:     r.Synthesised,
			NotaryJWS:       r.NotaryJWS,
			NotaryAvailable: r.NotaryJWS != "",
			ReceivedAt:      r.ReceivedAt.Format("2006-01-02T15:04:05.000Z07:00"),
		}
		if r.Accepted && r.AcceptedUse != "" && r.AcceptedUse != sess.InfluenceMode {
			view.Mismatch = true
		}
		if r.Turn == -1 {
			cp := view
			preSession = &cp
			continue
		}
		rcptByTurn[r.Turn] = view
	}

	turns := make([]sessionAuditTurn, 0, len(msgs))
	for _, m := range msgs {
		row := sessionAuditTurn{
			Turn:      m.Turn,
			Role:      m.Role,
			Content:   m.Content,
			CreatedAt: m.CreatedAt.Format("2006-01-02T15:04:05.000Z07:00"),
		}
		// Receipts are about brand turns — only brand rows carry one.
		if m.Role == "brand" {
			if rv, ok := rcptByTurn[m.Turn]; ok {
				rcpt := rv
				row.Receipt = &rcpt
			}
		}
		turns = append(turns, row)
	}

	writeJSON(w, http.StatusOK, sessionAuditView{
		SessionID:            sess.SessionID,
		SessionStatus:        sess.SessionStatus,
		BrandName:            h.cfg.Brand.Name,
		BrandDomain:          h.cfg.Brand.Domain,
		PayingPrincipalBrand: h.cfg.Brand.Domain, // canonical identity per M6.3 envelope
		DeclaredContextUse:   sess.InfluenceMode,
		OfferingID:           sess.OfferingID,
		Intent:               sess.Intent,
		ConsentGranted:       sess.ConsentGranted,
		CreatedAt:            sess.CreatedAt.Format("2006-01-02T15:04:05.000Z07:00"),
		UpdatedAt:            sess.UpdatedAt.Format("2006-01-02T15:04:05.000Z07:00"),
		Turns:                turns,
		PreSessionReceipt:    preSession,
	})
}

