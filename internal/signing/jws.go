package signing

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Envelope is the on-wire shape verify_brand_claim places under
// signed_response. It mirrors response-payload-jws-envelope.json:
// `payload` ships as a decoded JSON object (caller convenience) while
// the signature is computed from the JCS canonicalization of the same
// payload — verifiers re-canonicalize before checking.
type Envelope struct {
	Protected string          `json:"protected"`
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

// SignedPayload is the canonical task-body payload signed inside
// Envelope.payload per response-payload-jws-envelope.json#definitions/response_payload.
// task is fixed to "verify_brand_claim" for this profile.
type SignedPayload struct {
	Typ         string          `json:"typ"`
	Task        string          `json:"task"`
	BrandDomain string          `json:"brand_domain"`
	AgentURL    string          `json:"agent_url"`
	RequestHash string          `json:"request_hash"`
	Response    json.RawMessage `json:"response"`
}

// SignVerifyBrandClaim wraps the response in a signed envelope per the
// AdCP closed designated-task response-signing profile. canonicalReq is
// the JCS canonicalization of the verify_brand_claim request body the
// caller sent us — we SHA-256 it for `request_hash`.
//
// Returns an Envelope ready to be embedded as `signed_response` on the
// outgoing verify_brand_claim response.
func (s *Signer) SignVerifyBrandClaim(brandDomain, agentURL string, canonicalReq []byte, response any) (*Envelope, error) {
	respCanonical, err := Canonicalize(response)
	if err != nil {
		return nil, fmt.Errorf("sign: canonicalize response: %w", err)
	}
	var respRaw json.RawMessage = respCanonical

	reqHash := sha256.Sum256(canonicalReq)
	payload := SignedPayload{
		Typ:         "adcp-response-payload+jws",
		Task:        "verify_brand_claim",
		BrandDomain: brandDomain,
		AgentURL:    agentURL,
		RequestHash: "sha256:" + base64.RawURLEncoding.EncodeToString(reqHash[:]),
		Response:    respRaw,
	}
	payloadCanonical, err := Canonicalize(payload)
	if err != nil {
		return nil, fmt.Errorf("sign: canonicalize payload: %w", err)
	}

	header := map[string]any{
		"alg": "EdDSA",
		"kid": s.kid,
		"typ": "adcp-response-payload+jws",
	}
	headerCanonical, err := Canonicalize(header)
	if err != nil {
		return nil, fmt.Errorf("sign: canonicalize header: %w", err)
	}

	protected := base64.RawURLEncoding.EncodeToString(headerCanonical)
	signingInput := protected + "." + base64.RawURLEncoding.EncodeToString(payloadCanonical)
	sig := ed25519.Sign(s.priv, []byte(signingInput))

	return &Envelope{
		Protected: protected,
		Payload:   payloadCanonical,
		Signature: base64.RawURLEncoding.EncodeToString(sig),
	}, nil
}

// SignReceiptNotary mints a JWS over an opaque notary payload — used
// by M6.3 receipt persistence to record "bragent received this
// sponsored_context_receipt at T with content hash H, signed by
// bragent-key". Unlike SignVerifyBrandClaim this is not a
// task-response signature: there is no AdCP request to bind to and no
// designated-task profile. The protected header is therefore
// brand-specific (`adcp-bragent-receipt-notary+jws`) so verifiers
// don't accidentally treat it as an interop signature on a spec task.
//
// Returns the compact JWS string `header.payload.signature` ready to
// stash in store.Receipt.NotaryJWS.
func (s *Signer) SignReceiptNotary(payload any) (string, error) {
	payloadCanonical, err := Canonicalize(payload)
	if err != nil {
		return "", fmt.Errorf("notary: canonicalize payload: %w", err)
	}
	header := map[string]any{
		"alg": "EdDSA",
		"kid": s.kid,
		"typ": "adcp-bragent-receipt-notary+jws",
	}
	headerCanonical, err := Canonicalize(header)
	if err != nil {
		return "", fmt.Errorf("notary: canonicalize header: %w", err)
	}
	protected := base64.RawURLEncoding.EncodeToString(headerCanonical)
	payloadEnc := base64.RawURLEncoding.EncodeToString(payloadCanonical)
	signingInput := protected + "." + payloadEnc
	sig := ed25519.Sign(s.priv, []byte(signingInput))
	return protected + "." + payloadEnc + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// VerifyEnvelope re-derives the JWS signing input from a received
// Envelope and checks the signature against pub. Used in tests and any
// future cross-implementation conformance check.
func VerifyEnvelope(pub ed25519.PublicKey, env *Envelope) error {
	// Re-canonicalize the payload — caller may have shipped a
	// non-canonical JSON tree under payload (the spec allows it for
	// human readability), so we trust the JCS rerun.
	var tree any
	if err := json.Unmarshal(env.Payload, &tree); err != nil {
		return fmt.Errorf("verify: decode payload: %w", err)
	}
	payloadCanonical, err := Canonicalize(tree)
	if err != nil {
		return fmt.Errorf("verify: canonicalize payload: %w", err)
	}
	signingInput := env.Protected + "." + base64.RawURLEncoding.EncodeToString(payloadCanonical)
	sig, err := base64.RawURLEncoding.DecodeString(env.Signature)
	if err != nil {
		return fmt.Errorf("verify: decode signature: %w", err)
	}
	if !ed25519.Verify(pub, []byte(signingInput), sig) {
		return fmt.Errorf("verify: signature mismatch")
	}
	return nil
}
