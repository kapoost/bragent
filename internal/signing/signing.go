// Package signing implements the AdCP 3.1 closed designated-task
// response-signing profile required by verify_brand_claim.
//
// Crypto budget:
//   - alg: EdDSA (Ed25519). Single-curve, stdlib-only, no parameter
//     tuning, deterministic signatures. The spec's signing-profile
//     section is alg-agnostic; EdDSA is the simplest path that keeps
//     bragent on stdlib + no JOSE library.
//   - kty/crv: OKP / Ed25519. Public key published as RFC 8037 JWK.
//   - kid: RFC 7638 JWK SHA-256 thumbprint, base64url unpadded. Stable
//     across reboots — recomputed from the public key on each load.
//   - typ: adcp-response-payload+jws per response-payload-jws-envelope.
//
// Canonicalization: RFC 8785 / JCS. We ship a minimal canonicalizer
// because every payload we sign uses ASCII-only field names and never
// emits floating-point numbers — the two cases where JCS gets thorny.
// See jcs.go for the constraint set; a golden-vector test locks it.
//
// Storage: the keypair persists at signing_key_path in PKCS#8-style
// minimal layout (raw seed | newline | raw public key, both base64
// encoded for readability). Generated on first boot with file mode
// 0600. Operators can rotate by deleting the file — the next boot
// mints a fresh key and the kid changes.
package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Signer carries the keypair + memoized kid. Methods are read-only
// after construction; safe for concurrent use.
type Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	kid  string
}

// LoadOrCreate returns a Signer initialised from path or, if path
// doesn't exist, generates a fresh Ed25519 keypair and writes it.
// Parent directories are created with 0700; the key file is 0600.
//
// The returned Signer is immediately usable for Sign() and JWK().
func LoadOrCreate(path string) (*Signer, error) {
	if path == "" {
		return nil, errors.New("signing_key_path is required for brand-rights signing")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read signing key %s: %w", path, err)
		}
		return generate(path)
	}
	return parse(data)
}

func generate(path string) (*Signer, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 keypair: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir keystore: %w", err)
	}
	enc := base64.StdEncoding
	payload := enc.EncodeToString(priv.Seed()) + "\n" + enc.EncodeToString(pub) + "\n"
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		return nil, fmt.Errorf("write signing key %s: %w", path, err)
	}
	return newSigner(priv, pub), nil
}

func parse(data []byte) (*Signer, error) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return nil, errors.New("signing key file: expected two base64 lines (seed, public)")
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[0]))
	if err != nil {
		return nil, fmt.Errorf("decode seed: %w", err)
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil {
		return nil, fmt.Errorf("decode public: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("seed: want %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	if len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public: want %d bytes, got %d", ed25519.PublicKeySize, len(pub))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	// Sanity-check the loaded seed actually derives the stored public
	// key. Catches truncation / file corruption / hand-edit damage.
	derived := priv.Public().(ed25519.PublicKey)
	for i := range pub {
		if derived[i] != pub[i] {
			return nil, errors.New("signing key file: public/seed mismatch — refusing to boot")
		}
	}
	return newSigner(priv, ed25519.PublicKey(pub)), nil
}

func newSigner(priv ed25519.PrivateKey, pub ed25519.PublicKey) *Signer {
	return &Signer{priv: priv, pub: pub, kid: thumbprint(pub)}
}

// thumbprint computes the RFC 7638 JWK SHA-256 thumbprint of an
// Ed25519 public key. The canonical JWK uses only the required
// members (crv, kty, x), already in the correct lexicographic order.
func thumbprint(pub ed25519.PublicKey) string {
	x := base64.RawURLEncoding.EncodeToString(pub)
	canonical := `{"crv":"Ed25519","kty":"OKP","x":"` + x + `"}`
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// KeyID returns the stable RFC 7638 thumbprint used in protected
// headers and JWKS entries.
func (s *Signer) KeyID() string { return s.kid }

// JWK returns the public-key JWK for inclusion in /.well-known/jwks.json.
// Carries `use=sig` and `adcp_use=response-signing` so verifiers can
// scope the key to designated-task response-signing (per spec).
func (s *Signer) JWK() map[string]any {
	return map[string]any{
		"kty":      "OKP",
		"crv":      "Ed25519",
		"x":        base64.RawURLEncoding.EncodeToString(s.pub),
		"kid":      s.kid,
		"use":      "sig",
		"alg":      "EdDSA",
		"adcp_use": "response-signing",
	}
}

// PublicKey exposes the raw public key for tests / verification helpers.
func (s *Signer) PublicKey() ed25519.PublicKey { return s.pub }
