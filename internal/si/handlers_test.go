package si

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/kapoost/bragent/internal/config"
	"github.com/kapoost/bragent/internal/feed"
	"github.com/kapoost/bragent/internal/llm"
	"github.com/kapoost/bragent/internal/store"
)

// M5 spec contract: AdCP core/context.json says "context data is never
// parsed by AdCP agents — it's simply preserved and returned." The
// compliance storyboard asserts this with `field_value path:
// context.correlation_id` on the response. These tests lock the echo
// at the handler layer so a regression surfaces before the storyboard.

func TestGetOffering_EchoesContext_AndEmitsAvailabilityStatus(t *testing.T) {
	h := newTestHandlers(t)

	rawCtx := json.RawMessage(`{"correlation_id":"test-corr-001","trace":"abc"}`)
	rawReq, _ := json.Marshal(OfferingPreviewRequest{
		Query:      "tent",
		MaxResults: 2,
		Context:    rawCtx,
	})

	out, mcpErr := h.getOffering(context.Background(), rawReq)
	if mcpErr != nil {
		t.Fatalf("getOffering returned error: %+v", mcpErr)
	}
	resp, ok := out.(OfferingPreviewResponse)
	if !ok {
		t.Fatalf("unexpected response type: %T", out)
	}

	if !reflect.DeepEqual([]byte(resp.Context), []byte(rawCtx)) {
		t.Errorf("context not echoed verbatim:\n  in : %s\n  out: %s", rawCtx, resp.Context)
	}
	if len(resp.Offerings) == 0 {
		t.Fatalf("expected offerings from fixture catalog, got none")
	}
	for i, o := range resp.Offerings {
		if o.AvailabilityStatus == "" {
			t.Errorf("offering[%d] %q missing availability_status", i, o.OfferingID)
		}
	}
}

func TestInitiateSession_EchoesContext(t *testing.T) {
	h := newTestHandlers(t)

	rawCtx := json.RawMessage(`{"correlation_id":"init-corr-42"}`)
	rawReq, _ := json.Marshal(InitiateSessionRequest{
		Intent:     "looking for a tent",
		OfferingID: "tent-2p",
		Identity:   &Identity{ConsentGranted: true, UserPseudoID: "u-1"},
		Context:    rawCtx,
	})

	out, mcpErr := h.initiateSession(context.Background(), rawReq)
	if mcpErr != nil {
		t.Fatalf("initiateSession returned error: %+v", mcpErr)
	}
	resp := out.(InitiateSessionResponse)
	if !reflect.DeepEqual([]byte(resp.Context), []byte(rawCtx)) {
		t.Errorf("context not echoed:\n  in : %s\n  out: %s", rawCtx, resp.Context)
	}
	if resp.SessionStatus != "active" {
		t.Errorf("expected session_status=active, got %q", resp.SessionStatus)
	}
}

func TestCapabilities_EmitsCanonicalSpecialismID(t *testing.T) {
	h := newTestHandlers(t)
	caps := h.capabilities()

	// AAO comply runner iterates all specialisms on the wire and
	// rejects on any unknown ID. We emit ONLY the spec-canonical
	// `sponsored-intelligence` (3.1.0-rc.* AdCPSpecialism enum). The
	// M1 legacy `sponsored_intelligence.core` was dropped on the
	// canonical-only switch — no public host has been observed
	// matching on the underscored form.
	var hasUnderscored, hasHyphenated bool
	for _, s := range caps.Specialisms {
		if s == "sponsored_intelligence.core" {
			hasUnderscored = true
		}
		if s == "sponsored-intelligence" {
			hasHyphenated = true
		}
	}
	if !hasHyphenated {
		t.Error("missing spec-canonical hyphenated specialism ID")
	}
	if hasUnderscored {
		t.Error("legacy underscored specialism ID still emitted; AAO comply runner rejects on unknown IDs in the list")
	}
}

func TestAvailabilityFromFeed(t *testing.T) {
	cases := []struct {
		in   bool
		want AvailabilityStatus
	}{
		{true, AvailabilityAvailable},
		{false, AvailabilitySoldOut},
	}
	for _, tc := range cases {
		if got := availabilityFromFeed(tc.in); got != tc.want {
			t.Errorf("availabilityFromFeed(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// newTestHandlers wires the smallest viable stack: an in-memory SQLite
// store, the bundled fixture catalog, the deterministic Mock LLM. Tests
// stay hermetic — no network, no on-disk cache outside t.TempDir().
func newTestHandlers(t *testing.T) *Handlers {
	t.Helper()

	cfg := &config.Config{
		Brand: config.Brand{
			Name:   "Acme Outdoor",
			Domain: "shop.acme-outdoor.example",
		},
		Feed: config.Feed{
			URL:             "file://../../feeds/example.json",
			Format:          "json",
			CachePath:       t.TempDir() + "/feed.json",
			RefreshInterval: "1h",
		},
		Store: config.Store{Path: t.TempDir() + "/test.db"},
	}

	catalog, err := feed.New(cfg.Feed)
	if err != nil {
		t.Fatalf("feed.New: %v", err)
	}
	st, err := store.Open(cfg.Store.Path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	return NewHandlers(cfg, catalog, st, llm.NewMock())
}
