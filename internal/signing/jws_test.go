package signing

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignVerifyRoundtrip(t *testing.T) {
	s, err := LoadOrCreate(filepath.Join(t.TempDir(), "signing.ed25519"))
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}

	canonicalReq := []byte(`{"claim_type":"subsidiary","claim":{"subsidiary_domain":"acme.example"}}`)
	body := map[string]any{
		"claim_type":          "subsidiary",
		"verification_status": "unknown",
	}
	env, err := s.SignVerifyBrandClaim("brand.example", "https://brand.example/mcp", canonicalReq, body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Wire-format round-trip: marshal the envelope, parse it, verify.
	on, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	var reparsed Envelope
	if err := json.Unmarshal(on, &reparsed); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	if err := VerifyEnvelope(s.PublicKey(), &reparsed); err != nil {
		t.Errorf("round-trip verify failed: %v", err)
	}
}

func TestSignedEnvelope_PayloadFields(t *testing.T) {
	s, err := LoadOrCreate(filepath.Join(t.TempDir(), "signing.ed25519"))
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}

	env, err := s.SignVerifyBrandClaim(
		"brand.example",
		"https://brand.example/mcp",
		[]byte(`{"k":"v"}`),
		map[string]any{"claim_type": "subsidiary", "verification_status": "unknown"},
	)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	for _, k := range []string{"typ", "task", "brand_domain", "agent_url", "request_hash", "response"} {
		if _, ok := payload[k]; !ok {
			t.Errorf("payload missing required field %q", k)
		}
	}
	if payload["task"] != "verify_brand_claim" {
		t.Errorf("task = %v, want verify_brand_claim", payload["task"])
	}
	if rh, ok := payload["request_hash"].(string); !ok || rh[:7] != "sha256:" {
		t.Errorf("request_hash should start with sha256:, got %v", payload["request_hash"])
	}
}

func TestKeypair_PersistsAcrossLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.ed25519")
	a, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	b, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if a.KeyID() != b.KeyID() {
		t.Errorf("kid changed across loads: %s vs %s", a.KeyID(), b.KeyID())
	}
}

func TestKeypair_TamperDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.ed25519")
	if _, err := LoadOrCreate(path); err != nil {
		t.Fatalf("first load: %v", err)
	}
	// Mint a second keypair, then replace the first file's public-key
	// line with the second key's public — leaves the seed intact so
	// parse() should detect the seed/public mismatch and refuse to boot.
	other, err := LoadOrCreate(filepath.Join(t.TempDir(), "other.ed25519"))
	if err != nil {
		t.Fatalf("second keypair: %v", err)
	}
	otherPub := base64.StdEncoding.EncodeToString(other.PublicKey())

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		t.Fatalf("unexpected keystore shape: %s", raw)
	}
	lines[1] = otherPub
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write tampered: %v", err)
	}

	if _, err := LoadOrCreate(path); err == nil {
		t.Error("expected error on seed/public mismatch, got nil")
	}
}
