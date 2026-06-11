package brand

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/kapoost/bragent/internal/config"
	"github.com/kapoost/bragent/internal/signing"
)

func TestVerifyBrandClaim_StubReturnsSignedUnknown(t *testing.T) {
	signer, err := signing.LoadOrCreate(filepath.Join(t.TempDir(), "signing.ed25519"))
	if err != nil {
		t.Fatalf("LoadOrCreate signer: %v", err)
	}
	h := NewHandler(config.Brand{Name: "Acme", Domain: "acme.example"}, signer, nil)

	req := VerifyBrandClaimRequest{
		ClaimType: ClaimSubsidiary,
		Claim:     json.RawMessage(`{"subsidiary_domain":"branch.acme.example"}`),
		Context:   json.RawMessage(`{"correlation_id":"verify-001"}`),
	}
	rawReq, _ := json.Marshal(req)

	out, mcpErr := h.VerifyBrandClaim(context.Background(), rawReq)
	if mcpErr != nil {
		t.Fatalf("unexpected error: %+v", mcpErr)
	}
	resp, ok := out.(VerifyBrandClaimResponse)
	if !ok {
		t.Fatalf("unexpected response type: %T", out)
	}
	if resp.VerificationStatus != StatusUnknown {
		t.Errorf("stub policy should return unknown, got %q", resp.VerificationStatus)
	}
	if resp.ClaimType != ClaimSubsidiary {
		t.Errorf("claim_type not echoed: %q", resp.ClaimType)
	}
	if string(resp.Context) != string(req.Context) {
		t.Errorf("context not echoed:\n  in:  %s\n  out: %s", req.Context, resp.Context)
	}

	env, ok := resp.SignedResponse.(*signing.Envelope)
	if !ok {
		t.Fatalf("signed_response missing or wrong type: %T", resp.SignedResponse)
	}
	if err := signing.VerifyEnvelope(signer.PublicKey(), env); err != nil {
		t.Errorf("envelope verify failed: %v", err)
	}
}

func TestVerifyBrandClaim_RejectsUnknownClaimType(t *testing.T) {
	signer, _ := signing.LoadOrCreate(filepath.Join(t.TempDir(), "signing.ed25519"))
	h := NewHandler(config.Brand{Domain: "acme.example"}, signer, nil)

	rawReq, _ := json.Marshal(VerifyBrandClaimRequest{
		ClaimType: "made_up",
		Claim:     json.RawMessage(`{}`),
	})
	_, mcpErr := h.VerifyBrandClaim(context.Background(), rawReq)
	if mcpErr == nil {
		t.Fatal("expected error on unknown claim_type")
	}
}

func TestVerifyBrandClaim_RefusesWithoutSigner(t *testing.T) {
	h := NewHandler(config.Brand{Domain: "acme.example"}, nil, nil)
	_, mcpErr := h.VerifyBrandClaim(context.Background(), json.RawMessage(`{"claim_type":"subsidiary","claim":{}}`))
	if mcpErr == nil {
		t.Fatal("expected error when signer is nil")
	}
}
