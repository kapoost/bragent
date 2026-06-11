// Package brand implements the AdCP 3.1 brand-protocol surface served by
// bragent. M6.1 ships verify_brand_claim with full JWS response signing
// over JCS-canonicalized payloads; the claim store itself is intentionally
// minimal (always returns verification_status: "unknown") so an operator
// can stand up signing infrastructure today and populate the claim store
// in M6.2.
//
// Spec status (2026-06-11): brand-rights is `preview` in the
// AdCPSpecialism enum (3.1.0-rc.11). The wire shapes below track the
// rc.11 schemas verbatim — they may shift; type comments call out which
// fields are non-required so a schema bump doesn't silently break
// downstream encoders.
package brand

import "encoding/json"

// ClaimType is the request discriminator. Spec: verify-brand-claim-request.json
// (oneOf with `propertyName: "claim_type"`).
type ClaimType string

const (
	ClaimSubsidiary ClaimType = "subsidiary"
	ClaimParent     ClaimType = "parent"
	ClaimProperty   ClaimType = "property"
	ClaimTrademark  ClaimType = "trademark"
)

// VerificationStatus mirrors brand/verification-status.json. Not every
// status applies to every claim_type — see the task page for the
// per-claim subset.
type VerificationStatus string

const (
	StatusOwned         VerificationStatus = "owned"
	StatusPendingReview VerificationStatus = "pending_review"
	StatusTransferring  VerificationStatus = "transferring"
	StatusDisputed      VerificationStatus = "disputed"
	StatusNotOurs       VerificationStatus = "not_ours"
	StatusArchived      VerificationStatus = "archived"
	StatusLicensedIn    VerificationStatus = "licensed_in"
	StatusLicensedOut   VerificationStatus = "licensed_out"
	StatusUnknown       VerificationStatus = "unknown"
)

// VerifyBrandClaimRequest is the discriminated-union shape we accept on
// the wire. Claim is the raw payload because each claim_type carries a
// different inner schema (see ClaimSubsidiary / ClaimParent / etc. in
// the spec); the handler reparses Claim against the discriminated shape.
type VerifyBrandClaimRequest struct {
	ClaimType ClaimType       `json:"claim_type"`
	Claim     json.RawMessage `json:"claim"`
	Context   json.RawMessage `json:"context,omitempty"`
	Ext       json.RawMessage `json:"ext,omitempty"`
}

// VerifyBrandClaimResponse — success branch of the oneOf in
// verify-brand-claim-response.json. Signed via SignedResponse; an error
// branch returns ErrorResponse (errors[] populated, no claim_type /
// verification_status / signed_response).
type VerifyBrandClaimResponse struct {
	ClaimType          ClaimType          `json:"claim_type"`
	VerificationStatus VerificationStatus `json:"verification_status"`
	Details            map[string]any     `json:"details,omitempty"`
	ContextNote        string             `json:"context_note,omitempty"`
	Context            json.RawMessage    `json:"context,omitempty"`
	Ext                json.RawMessage    `json:"ext,omitempty"`
	// SignedResponse is required by the spec on success branches.
	// Handler populates it via signing.Signer.SignVerifyBrandClaim.
	SignedResponse any `json:"signed_response"`
}

// ErrorResponse — error branch of the oneOf in verify-brand-claim-response.json.
// The spec's `not` clause forbids `verification_status` / `claim_type` in this
// branch; we keep the shape minimal.
type ErrorResponse struct {
	Errors  []ErrorEntry    `json:"errors"`
	Context json.RawMessage `json:"context,omitempty"`
	Ext     json.RawMessage `json:"ext,omitempty"`
}

// ErrorEntry — single core/error.json entry. Only `code` + `message`
// are universally required; we leave the richer fields (target, hint,
// retry-after) out until a concrete need surfaces.
type ErrorEntry struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
