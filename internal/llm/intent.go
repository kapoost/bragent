package llm

import "strings"

// buyIntent is the cross-provider vocabulary used to promote a session
// from "active" to "pending_handoff". Lives in one place so the Mock and
// the OpenAI-compatible provider agree on when to hand off — the LLM
// itself does not advertise session status, so we infer it from the
// host's most recent turn.
var buyIntent = []string{
	"buy", "purchase", "order", "checkout", "add to cart", "where can i get",
	"how do i get one", "ready to buy", "let's do it", "go ahead",
}

// detectHandoff returns (true, handoffURL, handoffMessage) when the user
// text signals buy intent. The handoff URL is keyed on the offering when
// one was negotiated at si_initiate_session so the brand checkout lands
// on the right product page. brandName/brandDomain come from config.
func detectHandoff(userText, brandName, brandDomain, offeringID string) (bool, string, string) {
	low := strings.ToLower(userText)
	for _, kw := range buyIntent {
		if strings.Contains(low, kw) {
			handoff := "https://" + brandDomain + "/checkout"
			if offeringID != "" {
				handoff += "?offering=" + offeringID
			}
			msg := "Great — I'll hand you off to " + brandName + "'s checkout. Your conversation context is preserved so they pick up where we left off."
			return true, handoff, msg
		}
	}
	return false, "", ""
}
