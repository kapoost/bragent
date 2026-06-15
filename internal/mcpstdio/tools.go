package mcpstdio

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AutoReceiptMode controls how the bridge synthesises
// sponsored_context_receipt on outgoing si_initiate_session and
// si_send_message requests when running between a non-SI-aware host
// (Claude Desktop) and the bragent backend. The narada (M6.3) called
// this a per-bridge flag so the audit log can distinguish synthesised
// receipts from real host receipts via host_surface="bridge-synthesized".
//
//   - AutoReceiptAcceptPresentation (default): synthesise an "accepted"
//     receipt only when the prior brand response declared
//     context_use=presentation_only; reject otherwise. Mirrors the spec
//     posture that a host that cannot honour the declared use mode MUST
//     reject rather than down-scope.
//   - AutoReceiptAcceptAll: synthesise an "accepted" receipt for every
//     context_use the brand declared. Useful for demos where you want
//     the dual-trail visible regardless of mode.
//   - AutoReceiptRejectAll: always synthesise a "rejected" receipt.
//     Useful for stress-testing the audit-mismatch path.
//   - AutoReceiptOff: never synthesise. Receipts only flow when an
//     actual MCP client sends one — for our stdio flow that means
//     never, since Claude Desktop doesn't speak SI.
type AutoReceiptMode string

const (
	AutoReceiptAcceptPresentation AutoReceiptMode = "accept-presentation"
	AutoReceiptAcceptAll          AutoReceiptMode = "accept-all"
	AutoReceiptRejectAll          AutoReceiptMode = "reject-all"
	AutoReceiptOff                AutoReceiptMode = "null"
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
			"remembers the active session_id and synthesises sponsored_context_receipt " +
			"on each turn per the configured --auto-receipt policy.",
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
				"description, price, availability_status, plus a sponsored_context envelope " +
				"declaring paying_principal, context_use, and disclosure_obligation per AdCP " +
				"3.1.0-rc.14.",
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
				"bridge), the welcome message, and a sponsored_context envelope with the brand's " +
				"paying_principal and disclosure_obligation. The bridge will auto-synthesise a " +
				"sponsored_context_receipt on the NEXT si_send_message per its --auto-receipt policy.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"intent"},
				"properties": map[string]any{
					"intent":      stringProp("User's natural-language intent"),
					"offering_id": stringProp("Optional — specific product the user is interested in"),
				},
			},
		},
		{
			Name: "si_send_message",
			Description: "Continue the active brand-agent conversation. The bridge synthesises a " +
				"sponsored_context_receipt for the prior brand turn (if --auto-receipt is on) and " +
				"attaches it to the request, then returns the brand's reply plus a fresh " +
				"sponsored_context envelope. A handoff URL appears in the response when the brand " +
				"transitions to pending_handoff.",
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

	// Side-effects on bridge state: cache session_id and the freshly
	// emitted sponsored_context so the next outgoing request can carry a
	// synthesised receipt for it. Done after success only so a failed
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
// (method, params) pair the underlying handler expects.
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
		// Attach a synthesised receipt for the prior si_get_offering if
		// the bridge has one cached. This populates the spec's
		// "pre-session receipt" slot — turn = -1 server-side.
		if receipt := s.state.takePending(); receipt != nil {
			out["sponsored_context_receipt"] = receipt
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
		out := map[string]any{
			"session_id": sid,
			"message":    msg,
		}
		// Attach a synthesised receipt for the prior brand turn.
		if receipt := s.state.takePending(); receipt != nil {
			out["sponsored_context_receipt"] = receipt
		}
		return "si_send_message", mustJSON(out), nil

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

// updateState handles the side-effects of caching session_id between
// stdio tool calls AND of stamping a synthesised sponsored_context_
// receipt for the just-received sponsored_context so the next outgoing
// request can carry it. Receipts are NOT synthesised when --auto-receipt
// is off, OR when the response had no sponsored_context envelope.
func (s *Server) updateState(name string, result any) {
	// Round-trip through JSON to a generic map so the field lookups
	// stay tool-name-agnostic and we don't have to import internal/si.
	m, ok := result.(map[string]any)
	if !ok {
		b, err := json.Marshal(result)
		if err != nil {
			return
		}
		m = map[string]any{}
		_ = json.Unmarshal(b, &m)
	}
	if name == "si_initiate_session" {
		if sid, ok := m["session_id"].(string); ok && sid != "" {
			s.state.set(sid)
		}
	}
	if name == "si_send_message" {
		if status, _ := m["session_status"].(string); status == "terminated" {
			s.state.set("")
		}
	}
	if name == "si_terminate_session" {
		s.state.set("")
	}
	// Cache the sponsored_context for the next outgoing receipt.
	if sc, ok := m["sponsored_context"].(map[string]any); ok && s.autoReceipt != AutoReceiptOff {
		s.state.setPending(s.synthesiseReceipt(sc))
	}
}

// synthesiseReceipt builds a sponsored_context_receipt from a freshly
// emitted sponsored_context and the configured AutoReceiptMode. The
// receipt always marks host_surface="bridge-synthesized" so the audit
// trail can distinguish bridge synthesis from real host receipts.
func (s *Server) synthesiseReceipt(sponsored map[string]any) map[string]any {
	declaredUse, _ := sponsored["context_use"].(string)
	obligation, _ := sponsored["disclosure_obligation"].(map[string]any)
	disclosureRequired := false
	if b, ok := obligation["required"].(bool); ok {
		disclosureRequired = b
	}

	accept := false
	switch s.autoReceipt {
	case AutoReceiptAcceptAll:
		accept = true
	case AutoReceiptRejectAll:
		accept = false
	case AutoReceiptAcceptPresentation:
		accept = declaredUse == "presentation_only"
	}

	receipt := map[string]any{
		"sponsored_context": sponsored,
		"host_receipt": map[string]any{
			"status":       map[bool]string{true: "accepted", false: "rejected"}[accept],
			"received_at":  time.Now().UTC().Format(time.RFC3339),
			"host_surface": "bridge-synthesized",
		},
	}
	hr := receipt["host_receipt"].(map[string]any)
	if accept {
		hr["accepted_context_use"] = declaredUse
		commit := map[string]any{}
		if disclosureRequired {
			commit["status"] = "accepted"
			if label, ok := obligation["label_text"].(string); ok {
				commit["label_text"] = label
			}
		} else {
			commit["status"] = "not_required"
		}
		hr["disclosure_commitment"] = commit
	} else {
		hr["rejection_reason"] = fmt.Sprintf("bridge --auto-receipt=%s does not accept context_use=%s", s.autoReceipt, declaredUse)
	}
	return receipt
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
