package mcpstdio

import (
	"context"
	"encoding/json"
	"fmt"
)

// initialize returns the standard MCP server handshake response.
// Capabilities advertise only `tools` — we don't ship resources or
// prompts, so we omit them rather than declare empty support.
func (s *Server) handleInitialize(req rpcRequest) (any, *rpcError) {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "bragent",
			"version": "0.1",
		},
		"instructions": "Brand-agent bridge over AdCP Sponsored Intelligence. " +
			"Call si_get_offering to preview the catalog, then si_initiate_session " +
			"to open a conversation, then si_send_message for each turn. The bridge " +
			"remembers the active session_id so you don't need to thread it.",
	}, nil
}

// toolDef is the MCP-spec shape returned by tools/list. Kept inline so
// adding a tool stays a single declarative entry below.
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (s *Server) handleToolsList() (any, *rpcError) {
	return map[string]any{"tools": s.tools()}, nil
}

func (s *Server) tools() []toolDef {
	stringProp := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	return []toolDef{
		{
			Name: "si_get_offering",
			Description: "Preview the brand's catalog. Returns matching offerings with title, " +
				"description, price, availability_status. Use before si_initiate_session to surface " +
				"what the brand has.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":       stringProp("Search query, e.g. 'ultralight 2-person tent'"),
					"max_results": map[string]any{"type": "integer", "default": 5},
				},
			},
		},
		{
			Name: "si_initiate_session",
			Description: "Open a brand-agent conversation. Returns session_id (remembered by the " +
				"bridge), the agent's welcome message, paying_principal (the entity that funds this " +
				"agent's inference — M6.2 disclosure), and the negotiated influence_mode. Call once " +
				"at the start of a brand interaction.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"intent"},
				"properties": map[string]any{
					"intent":      stringProp("User's natural-language intent"),
					"offering_id": stringProp("Optional — specific product the user is interested in"),
					"influence_mode": map[string]any{
						"type":        "string",
						"enum":        []string{"presentation_only", "reasoning_context", "comparison_set"},
						"default":     "presentation_only",
						"description": "How this session's outputs may participate in the host's reasoning (M6.2)",
					},
				},
			},
		},
		{
			Name: "si_send_message",
			Description: "Continue the active brand-agent conversation. Uses the session_id remembered " +
				"by the bridge. Returns the brand's reply, session_status, and a handoff URL when the " +
				"brand signals pending_handoff.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"message"},
				"properties": map[string]any{
					"message": stringProp("The user's turn for the brand agent"),
				},
			},
		},
		{
			Name: "si_terminate_session",
			Description: "End the active brand-agent conversation and forget the session_id so the " +
				"next si_initiate_session starts fresh.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{"type": "string", "default": "user_exit"},
				},
			},
		},
	}
}

// handleToolsCall is the MCP equivalent of HTTP /mcp dispatch — it
// unpacks the tool name + arguments, builds the AdCP-shape params that
// si.Handlers expects, calls Handle in-process, and wraps the result
// in MCP's content array shape.
func (s *Server) handleToolsCall(ctx context.Context, req rpcRequest) (any, *rpcError) {
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &call); err != nil {
		return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
	}
	if call.Arguments == nil {
		call.Arguments = map[string]any{}
	}

	method, params, err := s.mapTool(call.Name, call.Arguments)
	if err != nil {
		return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
	}

	result, rpcErr := s.handler.Handle(ctx, method, params)
	if rpcErr != nil {
		// AdCP error → MCP tool error (isError flag, not transport error)
		// so the model sees a recoverable tool failure rather than a
		// protocol-level abort.
		return toolError(rpcErr.Message), nil
	}

	// Side-effects on bridge state. Done after success only so a failed
	// si_initiate_session doesn't poison the session pointer.
	s.updateState(call.Name, result)

	body, mErr := json.MarshalIndent(result, "", "  ")
	if mErr != nil {
		return toolError("marshal result: " + mErr.Error()), nil
	}
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(body)},
		},
	}, nil
}

func toolError(msg string) map[string]any {
	return map[string]any{
		"isError": true,
		"content": []map[string]any{
			{"type": "text", "text": "Error: " + msg},
		},
	}
}

// mapTool translates the MCP tool name + arguments into the AdCP
// (method, params) pair the underlying handler expects. Centralised so
// adding a new tool doesn't require touching the dispatch.
func (s *Server) mapTool(name string, args map[string]any) (string, json.RawMessage, error) {
	switch name {
	case "si_get_offering":
		out := map[string]any{
			"query": str(args, "query"),
		}
		if v, ok := args["max_results"]; ok {
			out["max_results"] = v
		}
		return "si_get_offering", mustJSON(out), nil

	case "si_initiate_session":
		intent := str(args, "intent")
		if intent == "" {
			return "", nil, fmt.Errorf("intent required")
		}
		out := map[string]any{
			"intent": intent,
			"identity": map[string]any{
				"consent_granted": true,
				"user_pseudo_id":  "mcp-stdio-bridge",
				"user_language":   "en",
			},
		}
		if v := str(args, "offering_id"); v != "" {
			out["offering_id"] = v
		}
		if v := str(args, "influence_mode"); v != "" {
			out["influence_mode"] = v
		}
		return "si_initiate_session", mustJSON(out), nil

	case "si_send_message":
		sid := s.state.get()
		if sid == "" {
			return "", nil, fmt.Errorf("no active session — call si_initiate_session first")
		}
		msg := str(args, "message")
		if msg == "" {
			return "", nil, fmt.Errorf("message required")
		}
		return "si_send_message", mustJSON(map[string]any{
			"session_id": sid,
			"message":    msg,
		}), nil

	case "si_terminate_session":
		sid := s.state.get()
		if sid == "" {
			return "", nil, fmt.Errorf("no active session to terminate")
		}
		return "si_terminate_session", mustJSON(map[string]any{
			"session_id": sid,
			"reason":     strDefault(args, "reason", "user_exit"),
		}), nil

	default:
		return "", nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// updateState handles the side-effect of caching session_id between
// stdio tool calls. Mirror of the Python bridge's _state dict but in
// Go and protected by a mutex.
func (s *Server) updateState(name string, result any) {
	m, ok := result.(map[string]any)
	if !ok {
		// si.Handlers return typed structs — round-trip through JSON
		// to a generic map so the field lookups below stay tool-name-
		// agnostic. Cheap (these structs are tiny) and keeps mcpstdio
		// from importing internal/si types.
		b, err := json.Marshal(result)
		if err != nil {
			return
		}
		m = map[string]any{}
		_ = json.Unmarshal(b, &m)
	}
	switch name {
	case "si_initiate_session":
		if sid, ok := m["session_id"].(string); ok && sid != "" {
			s.state.set(sid)
		}
	case "si_send_message":
		if status, _ := m["session_status"].(string); status == "terminated" {
			s.state.set("")
		}
	case "si_terminate_session":
		s.state.set("")
	}
}

func str(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func strDefault(args map[string]any, key, def string) string {
	if v := str(args, key); v != "" {
		return v
	}
	return def
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
