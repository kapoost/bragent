// Package mcpstdio implements the Anthropic MCP protocol over stdio,
// bridging Claude Desktop (and any other MCP host that launches the
// binary via stdio) to the same si.Handlers that serve AdCP over HTTP.
//
// One Go binary, two wire formats:
//
//	$ bragent --config ...                    # HTTP /mcp + AdCP/SI methods (buyer agents)
//	$ bragent --mcp-stdio --config ...        # stdio + Anthropic MCP (Claude Desktop)
//
// Both modes share the catalog, the SQLite session store, the LLM
// provider, and the dispatch into the SI handlers — so a session
// opened from stdio is the same session shape that would be opened
// from HTTP, including paying_principal disclosure and influence_mode
// negotiation.
//
// MCP protocol shape implemented (spec rev 2024-11-05 / 2025-06-18,
// minimal viable subset):
//
//   - initialize          → returns serverInfo + capabilities.tools{}
//   - notifications/initialized   → noop (no response — it is a notification)
//   - tools/list          → returns the four SI tools + verify_brand_claim
//                          when brand-rights signing is wired
//   - tools/call          → dispatches into the underlying mcp.Handler
//
// Anything else returns method-not-found. We intentionally don't ship
// resources/* or prompts/* — the brand-agent surface doesn't need them
// and shipping unused capabilities just confuses model schedulers.
//
// stdio transport rules (per the MCP spec):
//   - Each JSON-RPC message is newline-delimited (no Content-Length).
//   - stdout is reserved for protocol traffic only.
//   - All logging goes to stderr.
package mcpstdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"

	"github.com/kapoost/bragent/internal/mcp"
)

// SessionState carries per-stdio-connection mutable state that the
// underlying SI handlers don't know about. The active session_id is
// the only thing we hold — once the host calls si_initiate_session,
// subsequent si_send_message / si_terminate_session calls don't need
// the host to thread the session_id through every Claude turn.
type SessionState struct {
	mu        sync.Mutex
	sessionID string
}

func (s *SessionState) get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

func (s *SessionState) set(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = id
}

// Server runs the stdio loop. Construct with the same mcp.Handler the
// HTTP server uses; the bridging happens inside Run.
type Server struct {
	handler mcp.Handler
	state   *SessionState
	in      *bufio.Reader
	out     io.Writer
	writeMu sync.Mutex // stdout writes must be serialised
}

func New(h mcp.Handler) *Server {
	return &Server{
		handler: h,
		state:   &SessionState{},
		in:      bufio.NewReader(os.Stdin),
		out:     os.Stdout,
	}
}

// Run blocks reading newline-delimited JSON-RPC frames from stdin and
// writing responses to stdout until EOF (Claude Desktop closes the
// pipe when shutting down) or ctx cancellation.
//
// Processing is synchronous on purpose: MCP stdio hosts (including
// Claude Desktop) issue tool calls sequentially and expect each
// response before sending the next. Going concurrent here just risks
// a goroutine's response missing the EOF-shutdown window with no UX
// upside.
func (s *Server) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := s.in.ReadBytes('\n')
		if len(line) > 0 {
			s.handle(ctx, line)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("stdio read: %w", err)
		}
	}
}

func (s *Server) handle(ctx context.Context, raw []byte) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.writeError(nil, errParse, "parse error: "+err.Error())
		return
	}
	// MCP notifications (no id) are fire-and-forget — never reply.
	if req.ID == nil {
		s.handleNotification(req)
		return
	}
	res, rpcErr := s.dispatch(ctx, req)
	if rpcErr != nil {
		s.writeError(req.ID, rpcErr.Code, rpcErr.Message)
		return
	}
	s.writeResult(req.ID, res)
}

func (s *Server) handleNotification(req rpcRequest) {
	// `notifications/initialized` is the only one we expect — and we
	// don't need to do anything in response. Logged for visibility.
	log.Printf("mcp-stdio notification: %s", req.Method)
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList()
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	case "ping":
		// MCP ping is an empty-payload health check that just returns {}.
		return map[string]any{}, nil
	default:
		return nil, &rpcError{Code: errMethodNotFound, Message: "method not found: " + req.Method}
	}
}

func (s *Server) writeResult(id json.RawMessage, result any) {
	s.write(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) writeError(id json.RawMessage, code int, message string) {
	s.write(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

func (s *Server) write(resp rpcResponse) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	b, err := json.Marshal(resp)
	if err != nil {
		log.Printf("mcp-stdio marshal error: %v", err)
		return
	}
	b = append(b, '\n')
	if _, err := s.out.Write(b); err != nil {
		log.Printf("mcp-stdio write error: %v", err)
	}
}

// JSON-RPC error codes (per MCP spec / JSON-RPC 2.0).
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
