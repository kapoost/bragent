package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/kapoost/bragent/internal/config"
)

// Handler dispatches a single JSON-RPC method. Returns (result, nil) on
// success or (nil, *Error) on failure — *Error is wire-shaped already.
type Handler interface {
	Handle(ctx context.Context, method string, params json.RawMessage) (any, *Error)
}

type Server struct {
	cfg     config.Server
	handler Handler
	http    *http.Server
}

func NewServer(cfg config.Server, h Handler) *Server {
	return &Server{cfg: cfg, handler: h}
}

func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	mux.HandleFunc("/.well-known/healthz", s.handleHealthz)

	s.http = &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		err := s.http.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintln(w, `{"ok":true}`)
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, nil, &Error{Code: ErrParse, Message: err.Error()})
		return
	}
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, nil, &Error{Code: ErrParse, Message: err.Error()})
		return
	}
	if req.JSONRPC != "2.0" {
		writeErr(w, req.ID, &Error{Code: ErrInvalidRequest, Message: `jsonrpc must be "2.0"`})
		return
	}
	if req.Method == "" {
		writeErr(w, req.ID, &Error{Code: ErrInvalidRequest, Message: "method required"})
		return
	}

	result, rpcErr := s.handler.Handle(r.Context(), req.Method, req.Params)
	if rpcErr != nil {
		log.Printf("mcp method=%s err=%d %s", req.Method, rpcErr.Code, rpcErr.Message)
		writeErr(w, req.ID, rpcErr)
		return
	}
	writeOK(w, Response{JSONRPC: "2.0", ID: req.ID, Result: result})
}

func writeOK(w http.ResponseWriter, resp Response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeErr(w http.ResponseWriter, id json.RawMessage, e *Error) {
	writeOK(w, Response{JSONRPC: "2.0", ID: id, Error: e})
}
