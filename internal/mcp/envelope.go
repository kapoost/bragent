package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// EnvelopeHandler wraps a native AdCP JSON-RPC handler and intercepts
// MCP-protocol methods (`initialize`, `tools/list`, `tools/call`) before
// they reach the inner dispatcher. Native AdCP methods (e.g. `si_get_offering`,
// `get_adcp_capabilities`) fall through unchanged — adopters that already
// drive bragent via raw JSON-RPC keep working.
//
// The wrap is what AAO comply runners (and any other MCP-aware buyer SDK)
// require to discover tools. Without it, AAO probe of bragent's `/mcp`
// endpoint sees `tools/call: unknown method` and reports the agent as
// "unreachable — Failed to discover MCP endpoint".
//
// Mirrors the in-process logic in `internal/mcpstdio` (which has done the
// same wrap for the Claude-Desktop stdio transport since M6.3) — the
// transport differs, the protocol envelope is the same.
type EnvelopeHandler struct {
	inner       Handler
	tools       []ToolDef
	serverName  string
	serverVer   string
	instructions string
}

// ToolDef matches the MCP `tools/list` element shape. InputSchema is a raw
// JSON object so callers can declare arbitrarily-nested JSON-Schema without
// forcing the wrap to know about every leaf type.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// NewEnvelopeHandler builds the wrapper. `inner` MUST be the native AdCP
// handler that dispatches on method name (e.g. `*si.Handlers`).
func NewEnvelopeHandler(inner Handler, tools []ToolDef, name, version, instructions string) *EnvelopeHandler {
	return &EnvelopeHandler{
		inner:        inner,
		tools:        tools,
		serverName:   name,
		serverVer:    version,
		instructions: instructions,
	}
}

func (e *EnvelopeHandler) Handle(ctx context.Context, method string, params json.RawMessage) (any, *Error) {
	switch method {
	case "initialize":
		return e.handleInitialize()
	case "notifications/initialized":
		// MCP notification — no response expected. The HTTP transport
		// here always returns a body, so we send an empty success.
		// (Stdio handler is special-cased to skip the response entirely.)
		return map[string]any{}, nil
	case "tools/list":
		return e.handleToolsList()
	case "tools/call":
		return e.handleToolsCall(ctx, params)
	default:
		return e.inner.Handle(ctx, method, params)
	}
}

func (e *EnvelopeHandler) handleInitialize() (any, *Error) {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    e.serverName,
			"version": e.serverVer,
		},
		"instructions": e.instructions,
	}, nil
}

func (e *EnvelopeHandler) handleToolsList() (any, *Error) {
	return map[string]any{"tools": e.tools}, nil
}

func (e *EnvelopeHandler) handleToolsCall(ctx context.Context, params json.RawMessage) (any, *Error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, &Error{Code: ErrInvalidParams, Message: "tools/call params: " + err.Error()}
	}
	if call.Name == "" {
		return nil, &Error{Code: ErrInvalidParams, Message: "tools/call: name required"}
	}

	// Native handlers expect their canonical AdCP arguments at the top
	// level of `params` (not nested under a wrapper). Forward the
	// arguments object verbatim. Empty arguments → empty object.
	args := call.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}

	result, rpcErr := e.inner.Handle(ctx, call.Name, args)
	if rpcErr != nil {
		// AdCP-side error → MCP tool error (isError flag on the result,
		// not a transport-level JSON-RPC error). MCP clients render the
		// failure as a recoverable tool error rather than aborting the
		// session.
		return map[string]any{
			"isError": true,
			"content": []map[string]any{
				{"type": "text", "text": fmt.Sprintf("Error: %s", rpcErr.Message)},
			},
		}, nil
	}

	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, &Error{Code: ErrInternal, Message: "marshal: " + err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(body)},
		},
		// `structuredContent` mirrors the AdCP result as a parsed object
		// alongside the text-content rendering. MCP clients that prefer
		// structured access (e.g. AAO comply runner extracting
		// context_outputs) read this instead of re-parsing the text body.
		"structuredContent": result,
	}, nil
}
