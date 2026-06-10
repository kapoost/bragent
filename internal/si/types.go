// Package si implements AdCP 3.0 Sponsored Intelligence handlers.
//
// Spec status (2026-06-09): sponsored_intelligence.core is experimental.
// si_initiate_session has a partial example schema published; si_get_offering,
// si_send_message, si_terminate_session are TBD. Types below are best-effort
// interpretation aligned with the published flow — expect refactors as the
// spec settles between 3.x releases.
//
// Reference: https://docs.adcontextprotocol.org/docs/sponsored-intelligence
package si

// OfferingPreviewRequest — input to si_get_offering. No user PII; the
// task is the pre-consent preview, designed to be called by the host
// before asking the user "want me to connect you with their assistant?"
type OfferingPreviewRequest struct {
	Query       string   `json:"query,omitempty"`
	PlacementID string   `json:"placement_id,omitempty"`
	MaxResults  int      `json:"max_results,omitempty"`
	Locale      string   `json:"locale,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type OfferingPreviewResponse struct {
	Offerings     []Offering `json:"offerings"`
	OfferingToken string     `json:"offering_token"`
	BrandName     string     `json:"brand_name"`
	BrandDomain   string     `json:"brand_domain"`
	Disclaimer    string     `json:"disclaimer,omitempty"`
}

type Offering struct {
	OfferingID  string  `json:"offering_id"`
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	Price       float64 `json:"price,omitempty"`
	Currency    string  `json:"currency,omitempty"`
	URL         string  `json:"url,omitempty"`
	Available   bool    `json:"available"`
}

// CapabilitiesResponse — what get_adcp_capabilities returns for this brand
// agent. specialisms/supported_protocols values track AdCP 3.0.
type CapabilitiesResponse struct {
	AdCPVersion        string   `json:"adcp_version"`
	Role               string   `json:"role"`
	Specialisms        []string `json:"specialisms"`
	SupportedProtocols []string `json:"supported_protocols"`
	Capabilities       []string `json:"capabilities"`
	AgentName          string   `json:"agent_name"`
	AgentURL           string   `json:"agent_url"`
}

// InitiateSessionRequest — input to si_initiate_session. Matches the partial
// example schema in docs.adcontextprotocol.org/docs/sponsored-intelligence
// (2026-06-09): the host forwards the user's intent + per-user identity
// (subject to consent) + a media_buy_id or offering_id tying the session
// back to the seller's attribution flow.
type InitiateSessionRequest struct {
	Intent                string                 `json:"intent,omitempty"`
	Identity              *Identity              `json:"identity,omitempty"`
	MediaBuyID            string                 `json:"media_buy_id,omitempty"`
	Placement             string                 `json:"placement,omitempty"`
	OfferingID            string                 `json:"offering_id,omitempty"`
	OfferingToken         string                 `json:"offering_token,omitempty"`
	SupportedCapabilities map[string]interface{} `json:"supported_capabilities,omitempty"`
	Locale                string                 `json:"locale,omitempty"`
}

// Identity — host-side user identity attached to the session. consent_granted
// is the explicit user-consent flag; all other fields are pseudonymous handles
// the host may share once the user opted in.
type Identity struct {
	ConsentGranted bool   `json:"consent_granted"`
	UserPseudoID   string `json:"user_pseudo_id,omitempty"`
	UserSegment    string `json:"user_segment,omitempty"`
	UserLanguage   string `json:"user_language,omitempty"`
}

// InitiateSessionResponse — first turn of the brand-agent conversation.
// session_id is the correlation key for every subsequent si_send_message,
// si_terminate_session, and (if the conversation reaches checkout) the
// handoff URL the host hands back to the user.
type InitiateSessionResponse struct {
	SessionID     string                 `json:"session_id"`
	SessionStatus string                 `json:"session_status"`
	Response      SessionTurnResponse    `json:"response"`
	BrandName     string                 `json:"brand_name"`
	BrandDomain   string                 `json:"brand_domain"`
	Capabilities  map[string]interface{} `json:"capabilities,omitempty"`
}

// SessionTurnResponse — the brand agent's user-facing payload for a single
// conversation turn. message is the natural-language reply the host renders
// inline; ui_elements is the optional structured component bundle (see SI
// "UI components" experimental surface) that hosts able to render rich UI
// can present alongside the text.
type SessionTurnResponse struct {
	Message    string                   `json:"message"`
	UIElements []map[string]interface{} `json:"ui_elements,omitempty"`
}
