# Reference implementer check-in: what blocks AdCP 3.1 graduation from preview?

**Repo target:** `adcontextprotocol/adcp` (meta / coordination)
**Type:** Process question / readiness signal
**Status:** Open question — request feedback from spec maintainers + comply suite owners
**Applies to:** AdCP 3.1.0-rc.15 (current cache) / `@adcp/sdk@9.0.0` stable / AAO comply runner

## Summary

3.1 surface is largely landed and runnable. From a reference-implementer seat — `purrsonality-seller` against 3.1-rc.15 — the canonical scenarios I expected to break are now passing, and the residual failures cluster into well-understood categories that are either already fixed in flight (`#5664`/PR `#5675`), known runner-side gaps (test isolation across storyboards), or out-of-scope-by-design for single-publisher implementations.

This issue is a check-in, not a request: what does the WG actually need from reference implementers before 3.1 can graduate from preview to a target the comply runner treats as badge-eligible? Happy to verify specific scenarios if the answer is "more coverage signal on X".

## Concrete state on a working reference impl

`purrsonality-seller` (`https://seller.purrsonality.rocketscience.pl/mcp`), AAO eval target 3.1-rc (resolved 3.1.0-rc.15), per-track isolated runs:

| Track | Scenarios passing | Notes |
|---|---|---|
| Core Protocol | 51/54 | pagination_integrity_list_accounts × 2 (single-publisher constraint), version_negotiation/capabilities_advertise_and_echo (advisory) |
| Product Discovery | **3/3 PASS** | canonical_formats × 6 sub-steps + canonical_create_satisfaction × 3 |
| Media Buy Lifecycle | **74/75** | only `delivery_monitoring` outstanding; probe-confirmed wire shape is schema-valid, failure mode opaque without verbose eval output |
| Creative Management | 32/33 | `creative_lifecycle/list_and_filter` blocked by cross-storyboard state leak (not seller-resolvable) |
| Reporting & Delivery | 12/13 | `measurement_accountability/discover_with_required_metrics` needs richer fixture support |
| Error Handling | **10/10 SILENT** | `stale_response_advisory` including the gated `force_upstream_unavailable` advisories |
| Signals | SKIP | not applicable for this agent |

Source: <https://github.com/kapoost/purrsonality-seller-agent> (32 commits in a single day landed Phase D/H/E/F/G + cardinality + bug closure against `@adcp/sdk@9.0.0`).

## What this signal does and doesn't say

**Does say:**

- The 3.1 surfaces I exercised (multi-currency pricing filter, canonical format dual-emission, native-in-feed validation, dependency impairment with cardinality, stale-response advisory, format option satisfaction at create-time) are all implementable against the published cache.
- Failure modes that *did* show up are well-bucketed: spec-runner UX gaps ([#5707][i5707] → [#5664][i5664] → [PR #5675][p5675]), runner-side test isolation, single-publisher constraints, and one likely state-leak (`list_and_filter`).
- The schemas are reasonable. Most issues were where the runner's expectation diverged from the schema's optional/required boundary in a way the eval output didn't surface — fixable on either side without spec churn.

**Doesn't say:**

- Whether other specialism mixes hit different walls. `purrsonality-seller` is `sales-non-guaranteed` single-publisher. A `sales-guaranteed` multi-publisher implementing 3.1 might find different friction.
- Whether the SI surface is comparably ready. Our bragent (`sponsored_intelligence.core`) is on rc.x but hasn't been driven through 3.1-rc evals at the same intensity.
- Whether 3.1 schema churn has settled. The cache jumped between rc.12 and rc.15 during this work; whether rc.16 lands more breaking changes is unknown from outside.

## Questions for the WG

1. **Is "3.1 stable" gated on storyboard coverage, on reference implementations, on issue closures, or on a specific schema-stability window?** From outside, it's not obvious which lever needs to move.

2. **Is there a known list of scenarios that need reference-impl verification before graduation?** If `canonical_formats` and `dependency_impairment_cardinality` are the kind of scenarios that needed real implementers to find issues like #5707 and #5664, telling potential implementers "we need someone to drive scenario X" might shake out the remaining ones.

3. **Once 3.1 graduates, does the AAO comply runner automatically treat it as badge-eligible, or is that a separate AAO decision?** From the reference-implementer seat the two look coupled; if they aren't, the meaningful gates for "when does this stop being labeled diagnostic only" sit in different places.

4. **What's the WG's preferred posture for an implementer who has working 3.1-rc coverage and wants to make it visible?** Article in `docs.adcontextprotocol.org`? Comment on a specific issue? Posting eval scoreboards in a WG channel?

## What we'd commit to

- Re-run our isolated eval against rc.16+ as it lands; report any regressions.
- Re-verify [#5707][i5707]/[#5664][i5664] resolution post-[PR #5675][p5675] merge (will close as duplicate of #5664 if our `transition.from` workaround turns out to have been coincidental, which @danyliukmykola's PR isolation matrix already validated).
- Drive any specific scenario the WG calls out as needing reference-impl coverage, if it's within `sales-non-guaranteed` single-publisher reach.

References:

- Field report covering this work: `bragent/docs/community/article-aao-compliance-lessons.md` (extended postscript).
- Open issues from this implementer: [#5707][i5707], [#5701][i5701].
- Spec work in flight that closes a finding from this work: [#5664][i5664] / [PR #5675][p5675].

[i5707]: https://github.com/adcontextprotocol/adcp/issues/5707
[i5701]: https://github.com/adcontextprotocol/adcp/issues/5701
[i5664]: https://github.com/adcontextprotocol/adcp/issues/5664
[p5675]: https://github.com/adcontextprotocol/adcp/pull/5675
