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
