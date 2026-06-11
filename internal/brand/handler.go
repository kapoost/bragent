package brand

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kapoost/bragent/internal/config"
	"github.com/kapoost/bragent/internal/mcp"
	"github.com/kapoost/bragent/internal/signing"
)

// Handler dispatches verify_brand_claim. The signer is the load-bearing
// dependency — the spec marks signed_response REQUIRED on success
// branches, so the handler refuses to construct success responses if no
// signer is wired.
//
// PolicyStore is the future M6.2 extension point: a real implementation
// would query the brand's authoritative claim records and return a
// concrete status. M6.1 ships a stub that returns "unknown" — a
// spec-legal "I have no position" answer that consumers fall back from
// to crawl-based mutual-assertion inference. This keeps bragent
// surfaceable as a brand-protocol implementer without forcing operators
// to populate a claim store before signing infrastructure works.
type Handler struct {
	brand   config.Brand
	agentURL string
	signer  *signing.Signer
	policy  PolicyStore
}

type PolicyStore interface {
	Lookup(ctx context.Context, claimType ClaimType, claim json.RawMessage) (VerificationStatus, map[string]any, string)
}

// UnknownStore always returns ("unknown", nil, ""). The default M6.1 wiring.
type UnknownStore struct{}

func (UnknownStore) Lookup(_ context.Context, _ ClaimType, _ json.RawMessage) (VerificationStatus, map[string]any, string) {
	return StatusUnknown, nil, ""
}

func NewHandler(brand config.Brand, signer *signing.Signer, policy PolicyStore) *Handler {
	if policy == nil {
		policy = UnknownStore{}
	}
	return &Handler{
		brand:    brand,
		agentURL: "https://" + brand.Domain + "/mcp",
		signer:   signer,
		policy:   policy,
	}
}

// VerifyBrandClaim handles the verify_brand_claim MCP method.
func (h *Handler) VerifyBrandClaim(ctx context.Context, params json.RawMessage) (any, *mcp.Error) {
	if h.signer == nil {
		return nil, &mcp.Error{
			Code:    mcp.ErrInternal,
			Message: "verify_brand_claim requires signing — set [brand].signing_key_path",
		}
	}

	var req VerifyBrandClaimRequest
	if len(params) == 0 {
		return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: "verify_brand_claim: params required"}
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: err.Error()}
	}
	if !validClaimType(req.ClaimType) {
		return nil, &mcp.Error{
			Code:    mcp.ErrInvalidParams,
			Message: fmt.Sprintf("verify_brand_claim: unknown claim_type %q", req.ClaimType),
		}
	}
	if len(req.Claim) == 0 {
		return nil, &mcp.Error{Code: mcp.ErrInvalidParams, Message: "verify_brand_claim: claim required"}
	}

	status, details, note := h.policy.Lookup(ctx, req.ClaimType, req.Claim)

	resp := VerifyBrandClaimResponse{
		ClaimType:          req.ClaimType,
		VerificationStatus: status,
		Details:            details,
		ContextNote:        note,
		Context:            req.Context,
	}

	// signed_response: canonicalize the request body for request_hash,
	// then sign the canonical task-body payload. The spec requires
	// signed_response's inner `response` to match the unsigned response
	// fields verbatim — we pass the same struct so JCS canonicalization
	// produces identical bytes.
	canonicalReq, sigErr := signing.Canonicalize(req)
	if sigErr != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: "canonicalize request: " + sigErr.Error()}
	}
	// Build the inner success-payload shape (excludes signed_response
	// itself and envelope fields) and feed it to the signer.
	signedBody := signedResponseBody{
		ClaimType:          resp.ClaimType,
		VerificationStatus: resp.VerificationStatus,
		Details:            resp.Details,
		ContextNote:        resp.ContextNote,
		Context:            resp.Context,
	}
	env, sigErr := h.signer.SignVerifyBrandClaim(h.brand.Domain, h.agentURL, canonicalReq, signedBody)
	if sigErr != nil {
		return nil, &mcp.Error{Code: mcp.ErrInternal, Message: "sign: " + sigErr.Error()}
	}
	resp.SignedResponse = env
	return resp, nil
}

// signedResponseBody is the shape we hand the signer — excludes
// signed_response and any envelope fields per
// response-payload-jws-envelope.json#definitions/signed_success_payload.
// Kept private so callers can't accidentally embed signed_response in
// the to-be-signed body.
type signedResponseBody struct {
	ClaimType          ClaimType          `json:"claim_type"`
	VerificationStatus VerificationStatus `json:"verification_status"`
	Details            map[string]any     `json:"details,omitempty"`
	ContextNote        string             `json:"context_note,omitempty"`
	Context            json.RawMessage    `json:"context,omitempty"`
}

func validClaimType(c ClaimType) bool {
	switch c {
	case ClaimSubsidiary, ClaimParent, ClaimProperty, ClaimTrademark:
		return true
	}
	return false
}
