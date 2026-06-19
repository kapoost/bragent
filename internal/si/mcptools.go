// Tool descriptors exposed via MCP `tools/list`. Kept in `internal/si`
// alongside the native handlers so the canonical list of advertised
// methods has one source of truth — capabilities(), the stdio bridge
// (internal/mcpstdio/tools.go), and the HTTP MCP envelope all read this.
//
// Input schemas mirror the AdCP wire shape; the envelope forwards the
// `arguments` object verbatim to the native handler, so the schema here
// is the same shape native callers post under `params`.
package si

import (
	"encoding/json"

	"github.com/kapoost/bragent/internal/mcp"
)

// ToolDescriptors returns the MCP tool list for the configured handler
// surface. Brand-rights tools are included only when WithBrand was
// called, matching the visibility logic in capabilities().
func (h *Handlers) ToolDescriptors() []mcp.ToolDef {
	tools := []mcp.ToolDef{
		{
			Name:        "get_adcp_capabilities",
			Description: "Discovery: returns the agent role, specialisms, supported protocols, and the tool surface advertised for the configured brand identity.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "si_get_offering",
			Description: "Preview the brand's catalog. Returns matching offerings with title, description, price, availability_status, plus a sponsored_context envelope declaring paying_principal, context_use, and disclosure_obligation per AdCP 3.1.0-rc.14.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query, e.g. 'ultralight 2-person tent'"},
					"max_results": {"type": "integer", "default": 5},
					"context": {"type": "object", "additionalProperties": true}
				}
			}`),
		},
		{
			Name:        "si_initiate_session",
			Description: "Open a brand-agent conversation. Returns session_id + welcome + sponsored_context envelope. Required: intent (natural-language user goal) and identity.consent_granted (sponsored intelligence cannot run without explicit user consent).",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"required": ["intent"],
				"properties": {
					"intent": {"type": "string", "description": "User's natural-language intent"},
					"identity": {
						"type": "object",
						"properties": {
							"consent_granted": {"type": "boolean"},
							"user_pseudo_id": {"type": "string"},
							"user_language": {"type": "string"}
						}
					},
					"offering_id": {"type": "string", "description": "Optional — specific offering the user is interested in"},
					"locale": {"type": "string"},
					"sponsored_context_receipt": {"type": "object", "additionalProperties": true}
				}
			}`),
		},
		{
			Name:        "si_send_message",
			Description: "Continue an active brand-agent conversation. Returns the brand's reply plus a fresh sponsored_context envelope. A handoff URL appears in the response when the brand transitions to pending_handoff.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"required": ["session_id", "message"],
				"properties": {
					"session_id": {"type": "string"},
					"message": {"type": "string"},
					"sponsored_context_receipt": {"type": "object", "additionalProperties": true}
				}
			}`),
		},
		{
			Name:        "si_terminate_session",
			Description: "End an active brand-agent conversation. Returns the final session_status='terminated' and clears server-side state.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"required": ["session_id"],
				"properties": {
					"session_id": {"type": "string"},
					"reason": {"type": "string", "default": "user_exit"}
				}
			}`),
		},
	}

	if h.brand != nil {
		tools = append(tools, mcp.ToolDef{
			Name:        "verify_brand_claim",
			Description: "Brand-rights verification (M6.1). Returns a JWS-signed verdict on whether a claimed brand relationship (subsidiary | parent | property | trademark) holds for this brand identity.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"required": ["claim_type", "subject"],
				"properties": {
					"claim_type": {"type": "string", "enum": ["subsidiary", "parent", "property", "trademark"]},
					"subject": {"type": "object", "additionalProperties": true},
					"object": {"type": "object", "additionalProperties": true}
				}
			}`),
		})
	}
	return tools
}

// MCPInstructions is the welcome string returned to MCP `initialize`
// callers. Tells clients which tools to call in what order.
const MCPInstructions = "Brand-agent for AdCP Sponsored Intelligence. " +
	"Call get_adcp_capabilities to discover the surface, si_get_offering to preview the catalog, " +
	"si_initiate_session to open a conversation, si_send_message for each turn, " +
	"si_terminate_session to end. Every si_* response carries a sponsored_context envelope; " +
	"hosts that ingest sponsored content MUST emit a sponsored_context_receipt on the next call " +
	"per AdCP 3.1.0-rc.14 §Sponsored Context Accountability."
