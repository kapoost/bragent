package si

import (
	"context"
	"encoding/json"
	"log"

	"github.com/kapoost/bragent/internal/llm"
	"github.com/kapoost/bragent/internal/mcp"
	"github.com/kapoost/bragent/internal/store"
)

// sendMessage advances a conversation by one turn. The host posts the
// user's text; the brand agent appends it to the session log, asks the
// configured LLM provider for a reply, persists that reply, and returns
// the wire SessionTurnResponse plus the session_status the host should
// propagate downstream.
func (h *Handlers) sendMessage(ctx context.Context, params json.RawMessage) (any, *mcp.Error) {
	var req SendMessageRequest
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: err.Error()}
		}
	}
	if req.SessionID == "" {
		return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: "session_id required"}
	}
	if req.Message == "" {
		return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: "message required"}
	}
	// M6.3 — validate the optional incoming receipt before we touch the
	// session so a malformed receipt doesn't pollute the audit log.
	if req.SponsoredContextReceipt != nil {
		if err := req.SponsoredContextReceipt.Validate(); err != nil {
			return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: "sponsored_context_receipt: " + err.Error()}
		}
	}

	sess, err := h.store.GetSession(ctx, req.SessionID)
	if err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: err.Error()}
	}
	if sess == nil {
		return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: "session not found: " + req.SessionID}
	}
	if sess.SessionStatus == "terminated" {
		return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: "session already terminated: " + req.SessionID}
	}

	// Append the user turn first so failure to call the LLM still leaves
	// the conversation auditable.
	turn, err := h.store.NextTurn(ctx, req.SessionID)
	if err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: err.Error()}
	}

	// Persist the incoming receipt against the BRAND turn it
	// acknowledges (the prior brand response, turn = current-host-turn - 1).
	// Done before LLM call so even a slow upstream doesn't drop the
	// audit record.
	if req.SponsoredContextReceipt != nil {
		brandTurnAcked := turn - 1
		if brandTurnAcked < 0 {
			brandTurnAcked = 0 // first send_message acknowledges the welcome (turn 0)
		}
		if err := h.persistReceipt(ctx, req.SessionID, brandTurnAcked, req.SponsoredContextReceipt); err != nil {
			log.Printf("si: persist receipt for session=%s turn=%d failed: %v", req.SessionID, brandTurnAcked, err)
		}
	}
	if err := h.store.AppendMessage(ctx, store.Message{
		SessionID: req.SessionID,
		Turn:      turn,
		Role:      "host",
		Content:   req.Message,
	}); err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: err.Error()}
	}

	// Pull the full turn log into the LLM context so the responder can
	// reference earlier turns. M3 mock keeps this advisory; a real model
	// provider will fold the history into its prompt.
	history, err := h.store.ListMessages(ctx, req.SessionID)
	if err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: err.Error()}
	}
	llmTurns := make([]llm.Turn, 0, len(history))
	for _, m := range history {
		llmTurns = append(llmTurns, llm.Turn{Role: m.Role, Content: m.Content})
	}

	reply := h.llm.Reply(llm.ReplyRequest{
		BrandName:   h.cfg.Brand.Name,
		BrandDomain: h.cfg.Brand.Domain,
		OfferingID:  sess.OfferingID,
		UserText:    req.Message,
		History:     llmTurns,
		Catalog:     h.catalog.All(),
	})

	// Persist the brand turn.
	brandTurn := turn + 1
	if err := h.store.AppendMessage(ctx, store.Message{
		SessionID: req.SessionID,
		Turn:      brandTurn,
		Role:      "brand",
		Content:   reply.Message,
	}); err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: err.Error()}
	}

	// Promote the session status if the LLM flagged pending_handoff.
	if reply.SessionStatus != "" && reply.SessionStatus != sess.SessionStatus {
		if err := h.store.UpdateSessionStatus(ctx, req.SessionID, reply.SessionStatus); err != nil {
			return nil, &mcp.Error{Code: mcp.ErrInternal, Message: err.Error()}
		}
	}

	// M6.3 — emit a fresh sponsored_context envelope on each brand turn.
	// We could promote ContextUse on handoff turns (the handoff URL is
	// a rendered unit, presentation_only stays correct) or on
	// recommendation turns (comparison_set semantics) — for now we
	// default everything to presentation_only and let a future
	// llm.Provider hint richer modes per response.
	emitted := ContextUsePresentationOnly

	resp := SendMessageResponse{
		SessionID:        req.SessionID,
		SessionStatus:    reply.SessionStatus,
		Response:         SessionTurnResponse{Message: reply.Message},
		SponsoredContext: h.buildSponsoredContext(emitted),
		Context:          req.Context,
	}
	if reply.HandoffURL != "" {
		resp.Handoff = &HandoffInfo{URL: reply.HandoffURL, SessionID: req.SessionID}
	}
	return resp, nil
}

// terminateSession marks the session terminated and records the host's
// reason if provided. Idempotent: terminating an already-terminated
// session returns the same response without an error.
func (h *Handlers) terminateSession(ctx context.Context, params json.RawMessage) (any, *mcp.Error) {
	var req TerminateSessionRequest
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: err.Error()}
		}
	}
	if req.SessionID == "" {
		return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: "session_id required"}
	}

	sess, err := h.store.GetSession(ctx, req.SessionID)
	if err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: err.Error()}
	}
	if sess == nil {
		return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: "session not found: " + req.SessionID}
	}

	if sess.SessionStatus != "terminated" {
		if err := h.store.UpdateSessionStatus(ctx, req.SessionID, "terminated"); err != nil {
			return nil, &mcp.Error{Code: mcp.ErrInternal, Message: err.Error()}
		}
	}

	return TerminateSessionResponse{
		SessionID:     req.SessionID,
		Terminated:    true,
		SessionStatus: "terminated",
		Reason:        req.Reason,
		Context:       req.Context,
	}, nil
}
