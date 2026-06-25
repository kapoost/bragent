# `dependency_impairment/verify_impaired` fails silently when `impairment.transition.from` is absent — spec says SHOULD, runner treats as required

**Repo target:** `adcontextprotocol/adcp` (spec + comply runner UX)
**Type:** Runner UX / spec clarification
**Status:** Open question — request feedback from comply maintainers + dependency-impairment storyboard authors
**Applies to:** AdCP 3.1.0-rc.15 (AAO compliance cache) / @adcp/sdk 9.0.0 stable (verified 2026-06-25)

## Summary

The `media_buy_seller/dependency_impairment/verify_impaired` storyboard fails with **no specific assertion error** when a seller emits an `impairment` whose `transition` object contains only `to` (omitting the optional `from`). The schema (`core/impairment.json`) marks `from` as optional with `additionalProperties: false` on `transition` and `required: ["to"]` only. The eval output collapses to the step title — "Read the buy — health: impaired, impairment entry present" — without naming which of the four step validations failed (`response_schema`, `field_value media_buys[0].health`, `field_present media_buys[0].impairments[0]`, `field_contains media_buys[0].impairments[*]`).

Adding `transition.from: "approved"` (the documented baseline of the storyboard, recoverable from narrative context but absent from the formal validation set) flips the storyboard from fail to pass in a single deploy. **No assertion text in the eval output points to `from` as the missing field.**

This combines a spec-vs-runner mismatch with a runner UX gap: implementers cannot resolve the failure from the eval output alone.

## Concrete impact

`purrsonality-seller` 3.1-rc compliance, `media_buy` track, single 5-line addition:

| Impairment shape | media_buy track | dependency_impairment basic flow |
|---|---|---|
| `transition: { to: "rejected" }` (schema-valid, spec-SHOULD) | 67/75 | setup ✓ baseline ✓ transition ✓ **verify_impaired ✗** swap_recovery ✗ (cascade) |
| `transition: { from: "approved", to: "rejected" }` (schema-valid, spec-SHOULD-included) | **69/75** | setup ✓ baseline ✓ transition ✓ **verify_impaired ✓ swap_recovery ✓** |

A direct `curl` probe against the same agent returned the storyboard-exact wire shape **in both cases**:

```json
{
  "media_buys": [{
    "media_buy_id": "mb_xxx",
    "health": "impaired",
    "impairments": [{
      "impairment_id": "imp_mb_xxx_0_acme_dep_banner_001",
      "resource_type": "creative",
      "resource_id": "acme_dep_banner_001",
      "package_ids": ["mb_xxx_purr_result_card_v1"],
      "transition": { "to": "rejected" },        // ← schema-valid, runner-rejected
      "reason_code": "content_rejected",
      "reason": "Dependency-impairment scenario: forcing approved → rejected to exercise impairment.coherence.",
      "observed_at": "2026-06-25T18:51:26.737Z"
    }]
  }]
}
```

The shape passes the four named validations in `dependency_impairment.yaml`:

- `response_schema`: zod-passes against `GetMediaBuysResponseMediaBuySchema` (schemas.generated.js).
- `field_value media_buys[0].health = "impaired"` ✓
- `field_present media_buys[0].impairments[0]` ✓
- `field_contains media_buys[0].impairments[*]` with `{resource_type, resource_id, package_ids: ["$context.package_id"], transition: { to: "rejected" }}` — partial-match, all fields present ✓

…but the runner still grades the step `FAILED`. Eval text is the step title only.

## What I think is happening

The `impairment.coherence` cross-resource assertion (referenced in `core/impairment.json` description and `dependency_impairment.yaml` narrative) likely runs at the runner level rather than at the schema level. From the spec text:

> The pattern constraint blocks free-form garbage, and the impairment.coherence assertion validates that 'from' is a known serviceable value for the resource_type.

If `impairment.coherence` requires the forward+inverse rule pair to validate a known serviceable→offline edge for the resource type, an `impairment` with `transition: { to: "rejected" }` and no `from` cannot ground the inverse rule — the runner has no edge to verify against the resource_type's serviceable-state vocabulary. So it fails.

That's defensible. But the failure mode is invisible: the four storyboard validations all pass; the coherence assertion is implicit and unreported.

## Three possible directions

### A) Make `transition.from` required (spec change)

Tighten `core/impairment.json` to mark `from` as required when the resource was discovered in a known serviceable state. Keep optional only for the "discovered already offline" carve-out (property depublished from brand.json crawl, etc.).

- **Pro:** Schema and runner agree. Sellers who pass schema validation also pass runner validation.
- **Con:** Breaking change for sellers in flight. Some legitimate cases (discovered-offline resources) need a way to opt out, which complicates the schema.

### B) Surface `impairment.coherence` failures as named assertions in eval output

The runner emits `FAILED: ... → impairment.coherence: from missing or not in serviceable_states[creative]` instead of collapsing to the step title.

- **Pro:** Zero spec change. Sellers self-correct on the first failed eval. Generalizes to other cross-resource assertions (audience.coherence, catalog_item.coherence) the runner already runs.
- **Con:** Runner UX scope; requires per-assertion message templates; some assertions may be expensive to surface.

### C) Add the storyboard validation explicitly

`dependency_impairment.yaml`'s `verify_impaired` step gets a `field_present media_buys[0].impairments[0].transition.from` validation. Same for the cardinality variant.

- **Pro:** Quick fix; the storyboard documents itself; no spec change.
- **Con:** Doesn't generalize; future cross-resource assertions need the same per-storyboard handling.

## What I'd ask the WG

1. Is `impairment.coherence` indeed gating on `transition.from` presence, or is the failure mode elsewhere? Anyone with comply-runner internals access who can confirm the specific assertion that rejects `transition: { to: "rejected" }`?
2. Should `transition.from` be MUST when the resource has a known prior serviceable state? Is the "discovered already offline" carve-out concrete enough to keep `from` optional for that case alone?
3. If `from` stays optional in the schema, can the runner improve its assertion error surface so the failure mode is namable? (Generalizes beyond impairments.)

## Status quo workaround (what I shipped)

Emit `transition.from: "approved"` for every creative-rejection impairment — the documented baseline of every dependency_impairment scenario forces approved before transitioning to rejected, so `from` is always known. Six-line patch:

```typescript
transition: { from: 'approved', to: entry.status }
```

The comment in the code records the runner-vs-schema reasoning so the next maintainer (or me, six months from now) doesn't relitigate it.

## References

- Schema: `/schemas/3.1.0/core/impairment.json` (`transition.required: ["to"]`, `transition.additionalProperties: false`)
- Storyboard: `media_buy_seller/dependency_impairment/verify_impaired` (cache `3.1.0/protocols/media-buy/scenarios/dependency_impairment.yaml`)
- Related: `dependency_impairment_cardinality` shows the same pattern across 4 storyboards, all of which would benefit from the same fix
- Seller commit: <https://github.com/kapoost/purrsonality-seller-agent/commit/201b07e>
- Companion fix (out of scope for this issue, surfaced together): `swap_recovery` requires the rejected creative to still exist in the library after `sync_creatives` returns `action: failed` with `PROVENANCE_REQUIRED`; the recovery step's `force_creative_status: approved` on the replacement creative throws `NOT_FOUND` otherwise. See <https://github.com/kapoost/purrsonality-seller-agent/commit/da35d14>.
