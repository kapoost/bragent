package signing

import (
	"encoding/json"
	"strings"
	"testing"
)

// JCS contract: keys sorted lexicographically, no whitespace,
// integers serialized as their literal digit run. This test covers
// the ASCII / no-float subset that bragent signed payloads actually use.
func TestCanonicalize_BragentDomain(t *testing.T) {
	in := `{
		"verification_status": "owned",
		"claim_type": "subsidiary",
		"details": {"first_observed_by_house_at": "2026-06-11T10:00:00Z", "brand_id": "leaf_42"},
		"context": {"correlation_id": "verify-001"}
	}`

	var tree map[string]any
	if err := json.Unmarshal([]byte(in), &tree); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := Canonicalize(tree)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}

	if !strings.HasPrefix(string(got), `{"claim_type":`) {
		t.Errorf("keys should be sorted lexicographically, got: %s", got)
	}
	if strings.ContainsAny(string(got), " \n\t") {
		t.Errorf("whitespace leaked into canonical form: %q", got)
	}
	if !strings.Contains(string(got), `"details":{"brand_id":`) {
		t.Errorf("nested object keys not sorted: %s", got)
	}
}

func TestCanonicalize_Determinism(t *testing.T) {
	a := map[string]any{
		"b": "two",
		"a": []any{"x", "y"},
		"c": map[string]any{"z": 1, "y": 2, "x": 3},
	}
	b := map[string]any{
		"c": map[string]any{"y": 2, "x": 3, "z": 1},
		"a": []any{"x", "y"},
		"b": "two",
	}
	ac, err := Canonicalize(a)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	bc, err := Canonicalize(b)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if string(ac) != string(bc) {
		t.Errorf("not deterministic across input orderings:\n  a: %s\n  b: %s", ac, bc)
	}
	want := `{"a":["x","y"],"b":"two","c":{"x":3,"y":2,"z":1}}`
	if string(ac) != want {
		t.Errorf("unexpected canonical form:\n  got:  %s\n  want: %s", ac, want)
	}
}

func TestCanonicalize_RejectsFloats(t *testing.T) {
	if _, err := Canonicalize(map[string]any{"x": 1.5}); err == nil {
		t.Error("expected error on float64 input, got nil")
	}
}
