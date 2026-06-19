// Package si implements AdCP 3.0 Sponsored Intelligence handlers.
//
// Spec status (2026-06-15): the SI surface is still `x-status:
// experimental` in AdCP 3.1.0-rc.14, but PR #5501 "feat(si): add
// sponsored context accountability" (merged 2026-06-12) landed the
// canonical envelope for the disclosure primitives bragent had
// prototyped in M6.2. Types below track the rc.14 schemas:
//
//   - si-sponsored-context.json — the brand-side declaration envelope
//   - si-sponsored-context-receipt.json — the host-side acceptance record
//   - si-context-use.json — the use-mode enum (renamed from M6.2 InfluenceMode)
//
// M6.3 is the conformance pass: M6.2's top-level paying_principal URL
// and influence_mode enum are replaced by the nested SponsoredContext
// struct. The wire shape is now spec-conformant — there are no
// compatibility aliases (no downstream conformant hosts exist yet, so
// the alias cost would outlive any user it would have helped).
//
// Reference: https://docs.adcontextprotocol.org/docs/sponsored-intelligence
package si

import (
	"encoding/json"
	"fmt"
	"time"
)

// ContextUse is the host-side use mode for sponsored context, per
// /schemas/sponsored-intelligence/si-context-use.json (rc.14). Renamed
// from M6.2's InfluenceMode — same three values, same semantics. The
// declared-but-non-honoured posture (silent downgrade) is forbidden by
// spec: a host that cannot honour the declared use mode MUST reject
// the sponsored context instead of accepting under a narrower mode.
type ContextUse string

const (
	ContextUsePresentationOnly ContextUse = "presentation_only"
	ContextUseComparisonSet    ContextUse = "comparison_set"
	ContextUseReasoningContext ContextUse = "reasoning_context"
)

// SupportedContextUses is the closed set bragent will accept on the
// declaration side (when emitting) and validate on the receipt side
// (when accepting host echoes).
var SupportedContextUses = []ContextUse{
	ContextUsePresentationOnly,
	ContextUseComparisonSet,
	ContextUseReasoningContext,
}

func (c ContextUse) IsValid() bool {
	for _, x := range SupportedContextUses {
		if x == c {
			return true
		}
	}
	return false
}

// BrandRef mirrors /schemas/core/brand-ref.json — minimal subset
// bragent emits. Spec defines many optional inline-override fields
// (industries, data_subject_contestation, brand_kit_override) that we
// don't fold here; brand.json on the brand's domain stays the
// canonical source.
type BrandRef struct {
	Domain  string `json:"domain"`
	BrandID string `json:"brand_id,omitempty"`
}

// PayingPrincipal is the economic-accountability fact for the
// sponsored context — the "zero-th primitive" bragent proposed in M6.2
// and that landed in spec rc.14 as the nested object below. The
// canonical identity is the brand reference; account/operator/
// display_name are convenience fields for downstream rendering.
type PayingPrincipal struct {
	Brand       BrandRef         `json:"brand"`
	Account     *PrincipalAccount `json:"account,omitempty"`
	Operator    string           `json:"operator,omitempty"`
	DisplayName string           `json:"display_name,omitempty"`
}

// PrincipalAccount carries an optional seller-assigned account
// identifier — kept narrow on purpose so the canonical economic
// principal stays paying_principal.brand.
type PrincipalAccount struct {
	AccountID string `json:"account_id"`
}

// DisclosureObligation is the brand-declared disclosure contract the
// host MUST either honour or reject. Maps to Masse primitive #2 in
// the WG-SI discussion of 2026-06-11.
type DisclosureObligation struct {
	Required      bool                     `json:"required"`
	LabelText     string                   `json:"label_text,omitempty"`
	Timing        string                   `json:"timing,omitempty"`    // before_use | at_first_influenced_output | near_each_influenced_output
	Proximity     string                   `json:"proximity,omitempty"` // session_level | near_rendered_unit | near_influenced_output
	Jurisdictions []DisclosureJurisdiction `json:"jurisdictions,omitempty"`
}

// DisclosureJurisdiction names where the obligation applies. Country
// is ISO 3166-1 alpha-2, regulation is a free-form identifier
// (operators usually use canonical short names like "FTC-16-CFR-Part-255"
// or "EU-DSA-Art-26").
type DisclosureJurisdiction struct {
	Country    string `json:"country"`
	Region     string `json:"region,omitempty"`
	Regulation string `json:"regulation"`
}

// DeclaredBy names the agent that attached the sponsored_context
// declaration. For bragent this is always {role: "brand_agent",
// agent_url: <ours>}; other roles (seller, network, platform) are
// reserved for upstream intermediaries the spec allows.
type DeclaredBy struct {
	AgentURL string `json:"agent_url,omitempty"`
	Role     string `json:"role"` // brand_agent | seller | network | platform
}

// SponsoredContext is the full M6.3 envelope. Emitted on every SI
// response bragent produces because every bragent response IS the
// brand's voice — there is no "non-sponsored" mode for a brand agent.
type SponsoredContext struct {
	PayingPrincipal      PayingPrincipal      `json:"paying_principal"`
	ContextUse           ContextUse           `json:"context_use"`
	DisclosureObligation DisclosureObligation `json:"disclosure_obligation"`
	DeclaredAt           string               `json:"declared_at,omitempty"`
	DeclaredBy           *DeclaredBy          `json:"declared_by,omitempty"`
}

// HostReceipt is the receiving-surface accountability fact: what the
// host accepted (or rejected), and what disclosure commitment they
// made. Constructed in two cases:
//   - Real host sends it on the next request after receiving a
//     sponsored_context in our prior response.
//   - The MCP bridge synthesises it on behalf of a non-SI-aware host
//     (Claude Desktop), marked declared_by.role="bridge-synthesized"
//     so audit consumers can distinguish.
type HostReceipt struct {
	Status               string                `json:"status"` // accepted | rejected
	AcceptedContextUse   ContextUse            `json:"accepted_context_use,omitempty"`
	ReceivedAt           string                `json:"received_at"`
	HostSurface          string                `json:"host_surface,omitempty"`
	DisclosureCommitment *DisclosureCommitment `json:"disclosure_commitment,omitempty"`
	RejectionReason      string                `json:"rejection_reason,omitempty"`
}

// DisclosureCommitment captures how the host committed to honour the
// declared disclosure obligation. Required on accepted receipts when
// the declaration's required=true; otherwise must be {status:not_required}
// or absent.
type DisclosureCommitment struct {
	Status    string `json:"status"` // accepted | not_required
	LabelText string `json:"label_text,omitempty"`
	Notes     string `json:"notes,omitempty"`
}

// SponsoredContextReceipt is the wire shape host sends back in the
// next request, echoing the sponsored_context plus the host_receipt.
type SponsoredContextReceipt struct {
	SponsoredContext SponsoredContext `json:"sponsored_context"`
	HostReceipt      HostReceipt      `json:"host_receipt"`
}

// Validate enforces the spec's allOf constraints in Go (the schema
// expresses them via JSON-Schema if/then chains; we keep parity here
// so receipts arriving via any wire path see the same gate):
//
//   - status=accepted ⇒ accepted_context_use MUST equal sponsored_context.context_use
//   - status=accepted AND disclosure_obligation.required=true ⇒ disclosure_commitment.status MUST be "accepted"
//   - status=accepted AND disclosure_obligation.required=false ⇒ disclosure_commitment.status MAY be "not_required"
//   - status=rejected ⇒ accepted_context_use and disclosure_commitment MUST be absent
//
// Returns a descriptive error suitable for surfacing back through
// mcp.Error.
func (r *SponsoredContextReceipt) Validate() error {
	if r == nil {
		return nil
	}
	switch r.HostReceipt.Status {
	case "accepted":
		if !r.HostReceipt.AcceptedContextUse.IsValid() {
			return fmt.Errorf("accepted receipt missing accepted_context_use")
		}
		if r.HostReceipt.AcceptedContextUse != r.SponsoredContext.ContextUse {
			return fmt.Errorf(
				"accepted_context_use %q must match declared context_use %q (silent downgrade forbidden)",
				r.HostReceipt.AcceptedContextUse, r.SponsoredContext.ContextUse,
			)
		}
		if r.SponsoredContext.DisclosureObligation.Required {
			if r.HostReceipt.DisclosureCommitment == nil || r.HostReceipt.DisclosureCommitment.Status != "accepted" {
				return fmt.Errorf("required disclosure: host_receipt.disclosure_commitment.status must be \"accepted\"")
			}
		}
		if r.HostReceipt.RejectionReason != "" {
			return fmt.Errorf("accepted receipt must not carry rejection_reason")
		}
	case "rejected":
		if r.HostReceipt.AcceptedContextUse != "" {
			return fmt.Errorf("rejected receipt must not carry accepted_context_use")
		}
		if r.HostReceipt.DisclosureCommitment != nil {
			return fmt.Errorf("rejected receipt must not carry disclosure_commitment")
		}
	case "":
		return fmt.Errorf("host_receipt.status required")
	default:
		return fmt.Errorf("host_receipt.status must be \"accepted\" or \"rejected\" (got %q)", r.HostReceipt.Status)
	}
	if r.HostReceipt.ReceivedAt == "" {
		return fmt.Errorf("host_receipt.received_at required")
	}
	if _, err := time.Parse(time.RFC3339, r.HostReceipt.ReceivedAt); err != nil {
		return fmt.Errorf("host_receipt.received_at not RFC3339: %w", err)
	}
	return nil
}

// AvailabilityStatus mirrors enums/offering-availability-status.json from
// 3.1.0-rc.11. Unchanged in M6.3.
type AvailabilityStatus string

const (
	AvailabilityAvailable        AvailabilityStatus = "available"
	AvailabilityLimited          AvailabilityStatus = "limited"
	AvailabilitySoldOut          AvailabilityStatus = "sold_out"
	AvailabilityExpired          AvailabilityStatus = "expired"
	AvailabilityRegionRestricted AvailabilityStatus = "region_restricted"
	AvailabilityInactive         AvailabilityStatus = "inactive"
)

// OfferingPreviewRequest — input to si_get_offering.
type OfferingPreviewRequest struct {
	Query       string          `json:"query,omitempty"`
	PlacementID string          `json:"placement_id,omitempty"`
	MaxResults  int             `json:"max_results,omitempty"`
	Locale      string          `json:"locale,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	Context     json.RawMessage `json:"context,omitempty"`
	Ext         json.RawMessage `json:"ext,omitempty"`
}

// OfferingPreviewResponse now carries sponsored_context (M6.3) — the
// offering and matching_products are sponsored content entering the
// host boundary, so the declaration applies to the package as a whole.
type OfferingPreviewResponse struct {
	Offerings        []Offering        `json:"offerings"`
	OfferingToken    string            `json:"offering_token"`
	BrandName        string            `json:"brand_name"`
	BrandDomain      string            `json:"brand_domain"`
	Disclaimer       string            `json:"disclaimer,omitempty"`
	SponsoredContext *SponsoredContext `json:"sponsored_context,omitempty"`
	Context          json.RawMessage   `json:"context,omitempty"`
	Ext              json.RawMessage   `json:"ext,omitempty"`
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

// CapabilitiesResponse — M6.3 drops our M6.2-era PayingPrincipal +
// InfluenceModesSupported extension fields. The information now flows
// in every SI response via sponsored_context, and capabilities goes
// back to being a thin discovery surface.
type CapabilitiesResponse struct {
	AdCPVersion        string             `json:"adcp_version"`
	Role               string             `json:"role"`
	Specialisms        []string           `json:"specialisms"`
	SupportedProtocols []string           `json:"supported_protocols"`
	Capabilities       []string           `json:"capabilities"`
	AgentName          string             `json:"agent_name"`
	AgentURL           string             `json:"agent_url"`
	// AdCP version negotiation block. AAO comply runners + buyer agents
	// reading the prerelease tracks gate on `adcp.supported_versions` —
	// without it `evaluate_agent_quality(compliance_target=3.1-rc)`
	// returns "agent does not advertise support for that target".
	// Spec: get-adcp-capabilities.json `adcp` property; release-precision
	// strings (e.g. "3.0", "3.1", "3.1-beta") per AdCP §version-negotiation.
	AdCP AdCPCapabilities `json:"adcp"`
}

// AdCPCapabilities is the version-negotiation block returned on
// get_adcp_capabilities. Mirrors `/schemas/3.x/protocol/get-adcp-capabilities-response.json
// .properties.adcp`. `supported_major_versions` is deprecated; emit it
// for backwards compat with 3.x sellers that still read the legacy field.
type AdCPCapabilities struct {
	SupportedMajorVersions []string `json:"supported_major_versions"`
	SupportedVersions      []string `json:"supported_versions"`
}

// InitiateSessionRequest carries sponsored_context_receipt (M6.3) when
// the host accepted a prior si_get_offering response and wants to
// settle the audit trail in the same call that opens the session.
type InitiateSessionRequest struct {
	Intent                  string                   `json:"intent,omitempty"`
	Identity                *Identity                `json:"identity,omitempty"`
	MediaBuyID              string                   `json:"media_buy_id,omitempty"`
	Placement               string                   `json:"placement,omitempty"`
	OfferingID              string                   `json:"offering_id,omitempty"`
	OfferingToken           string                   `json:"offering_token,omitempty"`
	SupportedCapabilities   map[string]interface{}   `json:"supported_capabilities,omitempty"`
	Locale                  string                   `json:"locale,omitempty"`
	SponsoredContextReceipt *SponsoredContextReceipt `json:"sponsored_context_receipt,omitempty"`
	Context                 json.RawMessage          `json:"context,omitempty"`
	Ext                     json.RawMessage          `json:"ext,omitempty"`
}

// Identity — host-side user identity attached to the session.
type Identity struct {
	ConsentGranted bool   `json:"consent_granted"`
	UserPseudoID   string `json:"user_pseudo_id,omitempty"`
	UserSegment    string `json:"user_segment,omitempty"`
	UserLanguage   string `json:"user_language,omitempty"`
}

// InitiateSessionResponse carries the welcome turn's sponsored_context.
type InitiateSessionResponse struct {
	SessionID        string                 `json:"session_id"`
	SessionStatus    string                 `json:"session_status"`
	Response         SessionTurnResponse    `json:"response"`
	BrandName        string                 `json:"brand_name"`
	BrandDomain      string                 `json:"brand_domain"`
	Capabilities     map[string]interface{} `json:"capabilities,omitempty"`
	SponsoredContext *SponsoredContext      `json:"sponsored_context,omitempty"`
	Context          json.RawMessage        `json:"context,omitempty"`
	Ext              json.RawMessage        `json:"ext,omitempty"`
}

type SessionTurnResponse struct {
	Message    string                   `json:"message"`
	UIElements []map[string]interface{} `json:"ui_elements,omitempty"`
}

// SendMessageRequest carries the host's receipt for the brand's prior
// turn's sponsored_context.
type SendMessageRequest struct {
	SessionID               string                   `json:"session_id"`
	Message                 string                   `json:"message"`
	SponsoredContextReceipt *SponsoredContextReceipt `json:"sponsored_context_receipt,omitempty"`
	Context                 json.RawMessage          `json:"context,omitempty"`
	Ext                     json.RawMessage          `json:"ext,omitempty"`
}

// SendMessageResponse carries this turn's sponsored_context.
type SendMessageResponse struct {
	SessionID        string              `json:"session_id"`
	SessionStatus    string              `json:"session_status"`
	Response         SessionTurnResponse `json:"response"`
	Handoff          *HandoffInfo        `json:"handoff,omitempty"`
	SponsoredContext *SponsoredContext   `json:"sponsored_context,omitempty"`
	Context          json.RawMessage     `json:"context,omitempty"`
	Ext              json.RawMessage     `json:"ext,omitempty"`
}

type HandoffInfo struct {
	URL       string `json:"url"`
	SessionID string `json:"session_id"`
}

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
