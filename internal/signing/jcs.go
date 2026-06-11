package signing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Canonicalize emits a deterministic byte sequence for v per RFC 8785
// (JSON Canonicalization Scheme) — restricted to the JSON values that
// AdCP signed payloads actually contain.
//
// Restrictions vs full JCS:
//
//   - Numbers MUST be JSON integers (ints / int64 / json.Number "12").
//     ES6 ToString rules for floating-point are not implemented; we
//     reject float64 explicitly. AdCP signed payloads use only string
//     enums, timestamps as ISO-8601 strings, and integers — no floats.
//
//   - String escaping follows Go's encoding/json (RFC 8259), which is a
//     strict superset of JCS for ASCII and Latin-1. Field names in
//     AdCP signed payloads are ASCII-only (subsidiary_domain,
//     verification_status, etc.) so the byte sequence matches JCS for
//     this domain. A future caller emitting non-ASCII keys would need a
//     proper RFC 8785 implementation — we document this loud.
//
// The golden vector in jcs_test.go locks the contract against the
// spec's official example (https://datatracker.ietf.org/doc/html/rfc8785#section-3.2.3).
func Canonicalize(v any) ([]byte, error) {
	// Round-trip through encoding/json so struct → map[string]any
	// normalization happens once, then walk the resulting tree.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonicalize: marshal source: %w", err)
	}
	var tree any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("canonicalize: decode tree: %w", err)
	}
	var out bytes.Buffer
	if err := writeCanonical(&out, tree); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeCanonical(w *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		w.WriteString("null")
	case bool:
		if t {
			w.WriteString("true")
		} else {
			w.WriteString("false")
		}
	case string:
		// encoding/json emits RFC 8259 escapes which line up with JCS
		// for ASCII payloads; rely on it to avoid reimplementing the
		// escape table.
		b, err := json.Marshal(t)
		if err != nil {
			return err
		}
		w.Write(b)
	case json.Number:
		// Integers only. Reject anything with a decimal point or
		// exponent — we don't implement ES6 ToString.
		s := t.String()
		for _, c := range s {
			if c == '.' || c == 'e' || c == 'E' {
				return fmt.Errorf("canonicalize: floats unsupported (%q)", s)
			}
		}
		w.WriteString(s)
	case float64:
		return fmt.Errorf("canonicalize: float64 unsupported")
	case map[string]any:
		w.WriteByte('{')
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		// JCS specifies UTF-16 code unit ordering. For the ASCII-only
		// field names AdCP uses, byte order == code unit order, so a
		// plain string sort matches the spec for our domain.
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				w.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			w.Write(kb)
			w.WriteByte(':')
			if err := writeCanonical(w, t[k]); err != nil {
				return err
			}
		}
		w.WriteByte('}')
	case []any:
		w.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				w.WriteByte(',')
			}
			if err := writeCanonical(w, e); err != nil {
				return err
			}
		}
		w.WriteByte(']')
	default:
		return fmt.Errorf("canonicalize: unsupported type %T", v)
	}
	return nil
}
