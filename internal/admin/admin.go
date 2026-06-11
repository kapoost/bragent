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

	"github.com/kapoost/bragent/internal/feed"
	"github.com/kapoost/bragent/internal/mcp"
)

//go:embed static/*
var staticFS embed.FS

// Handler is the /admin/ multiplexer. Holds direct references to the
// catalog (for CRUD) and the SI dispatcher (for the chat panel). The
// chat panel calls SI handlers in-process — same code path as the wire
// MCP route, no second HTTP hop.
type Handler struct {
	token   string
	catalog *feed.Catalog
	si      mcp.Handler
	brand   string
	static  fs.FS
}

func New(token string, catalog *feed.Catalog, si mcp.Handler, brand string) *Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Compile-time guaranteed by the embed directive; panic on drift
		// rather than silently boot with no UI.
		panic("admin: embedded static missing: " + err.Error())
	}
	return &Handler{token: token, catalog: catalog, si: si, brand: brand, static: sub}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorised(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid X-Admin-Token"})
		return
	}
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
	case strings.HasPrefix(path, "/"):
		h.serveStatic(w, r, strings.TrimPrefix(path, "/"))
	default:
		http.NotFound(w, r)
	}
}

// authorised accepts the token in either the X-Admin-Token header (used
// by fetch() from the embedded JS) or a ?token=... query string (so a
// freshly-pasted URL works without JavaScript writing the header). The
// query path is dev-ergonomics only — production should use the header.
func (h *Handler) authorised(r *http.Request) bool {
	if h.token == "" {
		return false
	}
	if r.Header.Get("X-Admin-Token") == h.token {
		return true
	}
	if r.URL.Query().Get("token") == h.token {
		return true
	}
	return false
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
	w.Write(b)
}

type catalogView struct {
	Brand    string         `json:"brand"`
	Writable bool           `json:"writable"`
	Products []feed.Product `json:"products"`
}

func (h *Handler) listProducts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, catalogView{
		Brand:    h.brand,
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

