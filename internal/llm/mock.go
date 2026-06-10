// Package llm is the brand-agent reply generator.
//
// M3 ships a deterministic mock responder so the full SI lifecycle is
// testable offline — no OpenAI key, no Ollama, no flakey egress. A real
// provider (OpenAI-compatible HTTP, llama.cpp, vLLM, …) will slot in
// behind the same Provider interface in a later milestone, gated on the
// [llm] section of the TOML config.
//
// The mock's reply policy is intentionally crude: it scans the user turn
// for product-name keywords and for buy-intent verbs, and it nudges the
// session toward `pending_handoff` once intent is clear. That's enough to
// drive the spec's three SessionStatus values through the test harness.
package llm

import (
	"strconv"
	"strings"

	"github.com/kapoost/bragent/internal/feed"
)

type Provider interface {
	// Reply returns the next assistant turn plus the session status the
	// brand agent should advertise after it.
	Reply(req ReplyRequest) ReplyResponse
}

type ReplyRequest struct {
	BrandName  string
	BrandDomain string
	OfferingID string
	UserText   string
	History    []Turn
	Catalog    []feed.Product
}

type Turn struct {
	Role    string // "host" or "brand"
	Content string
}

type ReplyResponse struct {
	Message       string
	SessionStatus string // "active" | "pending_handoff" | "terminated"
	HandoffURL    string
}

// Mock is the offline responder. Keep it stateless — all conversational
// state lives in the session store; the mock just scores the most recent
// user turn against the product catalog plus a buy-intent vocabulary.
type Mock struct{}

func NewMock() *Mock { return &Mock{} }

var buyIntent = []string{
	"buy", "purchase", "order", "checkout", "add to cart", "where can i get",
	"how do i get one", "ready to buy", "let's do it", "go ahead",
}

func (m *Mock) Reply(req ReplyRequest) ReplyResponse {
	low := strings.ToLower(req.UserText)

	// Pending handoff: the user has signalled buy intent. Hand them a
	// brand checkout URL keyed on offering when we have one.
	for _, kw := range buyIntent {
		if strings.Contains(low, kw) {
			handoff := "https://" + req.BrandDomain + "/checkout"
			if req.OfferingID != "" {
				handoff += "?offering=" + req.OfferingID
			}
			return ReplyResponse{
				Message: "Great — I'll hand you off to " + req.BrandName + "'s checkout. Your conversation context is preserved so they pick up where we left off.",
				SessionStatus: "pending_handoff",
				HandoffURL:    handoff,
			}
		}
	}

	// Otherwise, see if the user mentioned a specific product. Pick the
	// best match (longest product name substring hit) and surface details
	// from the catalog row.
	best := pickProduct(low, req.Catalog)
	if best != nil {
		msg := best.Name + ": " + best.Description
		if best.Price > 0 {
			msg += " " + formatPrice(best.Price, best.Currency) + "."
		}
		if best.URL != "" {
			msg += " More: " + best.URL
		}
		return ReplyResponse{Message: msg, SessionStatus: "active"}
	}

	// Fallback acknowledgement so the host always has something to render.
	return ReplyResponse{
		Message: "Got it. I can pull up specs, compare options, or take you to checkout — what would help most?",
		SessionStatus: "active",
	}
}

func pickProduct(needleLower string, catalog []feed.Product) *feed.Product {
	var best *feed.Product
	bestLen := 0
	for i, p := range catalog {
		for _, candidate := range []string{p.Name, p.ID} {
			cl := strings.ToLower(candidate)
			if len(cl) >= 4 && strings.Contains(needleLower, cl) && len(cl) > bestLen {
				best = &catalog[i]
				bestLen = len(cl)
			}
		}
		for _, tag := range p.Tags {
			tl := strings.ToLower(tag)
			if len(tl) >= 4 && strings.Contains(needleLower, tl) && len(tl) > bestLen {
				best = &catalog[i]
				bestLen = len(tl)
			}
		}
	}
	return best
}

func formatPrice(amount float64, currency string) string {
	if currency == "" {
		currency = "USD"
	}
	return currency + " " + strconv.FormatFloat(amount, 'f', 2, 64)
}
