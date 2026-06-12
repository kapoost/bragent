// Package si implements AdCP 3.0 Sponsored Intelligence handlers.
//
// Spec status (2026-06-11): sponsored_intelligence.core is experimental
// in AdCP 3.0; full request/response shapes for all SI tools are
// published through 3.1.0-rc.11. Types below track the published
// schemas with a few intentional simplifications (e.g. our
// si_get_offering still returns `offerings[]` for parity with the M1
// catalog search rather than the spec's singular `offering` +
// optional `matching_products[]` shape — a follow-up will align).
//
// M5 additions (additive, backward-compatible):
//   - `availability_status` enum on Offering (3.1.0-rc.11)
//   - `context` / `ext` passthrough on every request/response (core
//     envelopes shipped at the spec level — context is opaque and
//     MUST be echoed unchanged on the response per core/context.json)
//
// Reference: https://docs.adcontextprotocol.org/docs/sponsored-intelligence
package si

import "encoding/json"

// InfluenceMode declares HOW sponsored context is intended to participate
// in the buyer-side reasoning chain — the M6.2 / experimental primitive
// proposed in #wg-campaign-sponsored-intelligence (2026-06-11). The
// brand agent publishes which modes it supports via capabilities; the
// host negotiates a mode at si_initiate_session and the brand agent
// echoes the agreed mode on every turn so the audit trail is per-turn.
//
//   - presentation_only: the agent's outputs MAY appear as a labelled
//     sponsored card, but MUST NOT be folded into the host's reasoning
//     substrate. Default and safest.
//   - reasoning_context: outputs MAY be consumed as evidence inside the
//     host model's reasoning chain. Carries a stronger disclosure
//     obligation on the host side.
//   - comparison_set: outputs are one of several comparable options
//     surfaced to the user; ranking is the host's responsibility.
//
// Spec note: this is not yet AdCP-canonical. We declare it under a
// brand-namespaced extension key so wire compatibility with hosts that
// don't know the field is preserved (they just ignore it).
type InfluenceMode string

const (
	InfluenceModePresentationOnly InfluenceMode = "presentation_only"
	InfluenceModeReasoningContext InfluenceMode = "reasoning_context"
	InfluenceModeComparisonSet    InfluenceMode = "comparison_set"
)

// SupportedInfluenceModes is the closed set advertised via capabilities.
// Adding a mode here requires also teaching the SI handlers to validate
// and persist it.
var SupportedInfluenceModes = []InfluenceMode{
	InfluenceModePresentationOnly,
	InfluenceModeReasoningContext,
	InfluenceModeComparisonSet,
}

// IsValid reports whether the mode is one this brand agent recognises.
// Unknown modes are rejected at si_initiate_session rather than silently
// downgraded — silent downgrade would defeat the audit-trail purpose.
func (m InfluenceMode) IsValid() bool {
	for _, x := range SupportedInfluenceModes {
		if x == m {
			return true
		}
	}
	return false
}

// AvailabilityStatus mirrors enums/offering-availability-status.json from
// 3.1.0-rc.11. Brand agents emit a structured availability state on each
// offering / matching product so hosts can distinguish "low stock" from
// "sold out" from "geo-restricted" without parsing free-text strings.
type AvailabilityStatus string

const (
	AvailabilityAvailable        AvailabilityStatus = "available"
	AvailabilityLimited          AvailabilityStatus = "limited"
	AvailabilitySoldOut          AvailabilityStatus = "sold_out"
	AvailabilityExpired          AvailabilityStatus = "expired"
	AvailabilityRegionRestricted AvailabilityStatus = "region_restricted"
	AvailabilityInactive         AvailabilityStatus = "inactive"
)

// OfferingPreviewRequest — input to si_get_offering. No user PII; the
// task is the pre-consent preview, designed to be called by the host
// before asking the user "want me to connect you with their assistant?"
//
// Context and Ext are open-scope passthroughs (AdCP core envelopes).
// Context is echoed verbatim in the response; Ext is a vendor-namespaced
// extension bag that we currently ignore but accept without erroring.
type OfferingPreviewRequest struct {
	Query       string          `json:"query,omitempty"`
	PlacementID string          `json:"placement_id,omitempty"`
	MaxResults  int             `json:"max_results,omitempty"`
	Locale      string          `json:"locale,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	Context     json.RawMessage `json:"context,omitempty"`
	Ext         json.RawMessage `json:"ext,omitempty"`
}

type OfferingPreviewResponse struct {
	Offerings     []Offering      `json:"offerings"`
	OfferingToken string          `json:"offering_token"`
	BrandName     string          `json:"brand_name"`
	BrandDomain   string          `json:"brand_domain"`
	Disclaimer    string          `json:"disclaimer,omitempty"`
	Context       json.RawMessage `json:"context,omitempty"`
	Ext           json.RawMessage `json:"ext,omitempty"`
}

type Offering struct {
	OfferingID         string             `json:"offering_id"`
	Title              string             `json:"title"`
	Description        string             `json:"description,omitempty"`
	Price              float64            `json:"price,omitempty"`
	Currency           string             `json:"currency,omitempty"`
	URL                string             `json:"url,omitempty"`
	Available          bool               `json:"available"`
	AvailabilityStatus AvailabilityStatus `json:"availability_status,omitempty"`
}

// CapabilitiesResponse — what get_adcp_capabilities returns for this brand
// agent. specialisms/supported_protocols values track AdCP 3.0.
//
// PayingPrincipal (M6.2) duplicates the brand.json field on purpose: hosts
// that have already issued a capabilities probe can render the trust
// badge without a second /.well-known/ fetch.
//
// InfluenceModesSupported (M6.2) advertises which influence_mode values
// si_initiate_session will accept. Hosts that don't recognise this field
// continue working — default mode is presentation_only either way.
type CapabilitiesResponse struct {
	AdCPVersion             string          `json:"adcp_version"`
	Role                    string          `json:"role"`
	Specialisms             []string        `json:"specialisms"`
	SupportedProtocols      []string        `json:"supported_protocols"`
	Capabilities            []string        `json:"capabilities"`
	AgentName               string          `json:"agent_name"`
	AgentURL                string          `json:"agent_url"`
	PayingPrincipal         string          `json:"paying_principal,omitempty"`
	InfluenceModesSupported []InfluenceMode `json:"influence_modes_supported,omitempty"`
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
	// InfluenceMode (M6.2) — host's negotiated stance on how this
	// session's outputs will participate in its reasoning chain. Default
	// presentation_only. Brand agent rejects unknown values.
	InfluenceMode InfluenceMode   `json:"influence_mode,omitempty"`
	Context       json.RawMessage `json:"context,omitempty"`
	Ext           json.RawMessage `json:"ext,omitempty"`
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
	// PayingPrincipal (M6.2) echoes brand.json so a host that initiated
	// without first crawling well-known can still render the trust badge
	// from the session-initiate response alone.
	PayingPrincipal string `json:"paying_principal,omitempty"`
	// InfluenceMode (M6.2) — agreed mode for this session. Always
	// non-empty in the response (defaulted to presentation_only when the
	// host didn't ask).
	InfluenceMode InfluenceMode   `json:"influence_mode,omitempty"`
	Context       json.RawMessage `json:"context,omitempty"`
	Ext           json.RawMessage `json:"ext,omitempty"`
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

// SendMessageRequest — input to si_send_message. The host forwards the
// user's latest utterance; the brand agent answers with the next turn
// and the session_status the host should propagate (active / pending_handoff
// / terminated). The host pins by session_id from si_initiate_session.
type SendMessageRequest struct {
	SessionID string          `json:"session_id"`
	Message   string          `json:"message"`
	Context   json.RawMessage `json:"context,omitempty"`
	Ext       json.RawMessage `json:"ext,omitempty"`
}

// SendMessageResponse mirrors the InitiateSessionResponse shape so the
// host can render either turn type identically. When SessionStatus is
// "pending_handoff" the Handoff block carries a checkout URL keyed on
// the brand domain; the host renders it as a CTA.
type SendMessageResponse struct {
	SessionID     string              `json:"session_id"`
	SessionStatus string              `json:"session_status"`
	Response      SessionTurnResponse `json:"response"`
	Handoff       *HandoffInfo        `json:"handoff,omitempty"`
	// InfluenceMode (M6.2) — echoed per-turn so each message in the host's
	// audit log carries the mode under which it was generated. Lets a
	// regulator answer "was this answer influenced by paid context, and
	// in what way?" from the wire trace alone.
	InfluenceMode InfluenceMode   `json:"influence_mode,omitempty"`
	Context       json.RawMessage `json:"context,omitempty"`
	Ext           json.RawMessage `json:"ext,omitempty"`
}

// HandoffInfo — the commerce destination the host hands the user back to
// when SessionStatus transitions to pending_handoff. session_id flows
// through so the brand's checkout can stitch the conversation context.
type HandoffInfo struct {
	URL       string `json:"url"`
	SessionID string `json:"session_id"`
}

// TerminateSessionRequest — graceful end-of-session signal from the host.
// reason mirrors the spec enum (handoff_transaction, handoff_complete,
// user_exit, session_timeout, host_terminated).
type TerminateSessionRequest struct {
	SessionID string          `json:"session_id"`
	Reason    string          `json:"reason,omitempty"`
	Context   json.RawMessage `json:"context,omitempty"`
	Ext       json.RawMessage `json:"ext,omitempty"`
}

type TerminateSessionResponse struct {
	SessionID     string          `json:"session_id"`
	SessionStatus string          `json:"session_status"`
	Reason        string          `json:"reason,omitempty"`
	Context       json.RawMessage `json:"context,omitempty"`
	Ext           json.RawMessage `json:"ext,omitempty"`
}
