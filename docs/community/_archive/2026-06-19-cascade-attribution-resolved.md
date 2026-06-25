# [ARCHIVED 2026-06-25] Comply runner: attribute cascade-skipped steps to their root-cause step_id

> **Status: ARCHIVED.** This draft was written 2026-06-19 when cascade skips printed only "prior stateful step failed" without naming the root step.
>
> **Why archived:** AAO runner now surfaces messages like `Skipped: prior stateful step "initiate_session" skipped (missing_tool); state never materialized.` — the exact attribution this draft proposed. Already shipped between draft creation and review.
>
> Kept as historical reference. Do not file as new GH issue — already implemented upstream.
>
> Original draft below.

---

**Repo target:** `adcontextprotocol/adcp` (or runner repo)
**Type:** Runner UX / report quality
**Affects:** Anyone reading `evaluate_agent_quality` JSON or `storyboard run` output to decide if a change made things better or worse

## Summary

When a storyboard's `setup` phase fails, downstream stateful steps in dependent scenarios print:
```
Skipped: prior stateful step failed.
Skipped: unresolved context variables from prior steps: product_id, pricing_option_id.
```

In the eval JSON these look like distinct failures across many scenarios. Reading the output before and after a fix gives a misleading impression of regression direction.

## Concrete from a recent eval cycle

Same seller, three back-to-back eval runs after sequential fixes:

| Run | media_buy track | Failures shown | Actual root causes |
|---|---|---|---|
| Baseline | 44/49 | 5 | 2 (schema-enum + setup failure) |
| Phase 1 (added 2nd product, broke catalog assumption) | 37/49 | 12 | 1 (catalog-size = 1 assumption) |
| Phase 1.1 (consolidated product) | 31/49 | 17 (cascade) | 1 (signing required for create_media_buy) |
| Phase 1.2 (signing relaxed) | **49/49** | 0 | — |

Each row's "failure count" is misleading because 90%+ of the failures were `Skipped: prior stateful step failed`. A reader sees "31 vs 44" and thinks the fix regressed — actually fewer scenarios reached the failing step, but each that did had the same root.

## Ask

When a step is skipped due to a prior step failing, attribute it in the output:

```yaml
FAILED: media_buy_seller/creative_fate_after_cancellation/cancel_buy
  - update_media_buy with canceled: true
    skipped: true
    skip_reason: "prior_step_failed"
    root_cause_step_id: "media_buy_seller/creative_fate_after_cancellation/setup#create_initial_media_buy"
    root_cause_error: "request_signature_required"
```

In the human summary, collapse cascade-skips into the root:

```
1 root failure → 11 cascaded skips:
  media_buy_seller/creative_fate_after_cancellation/setup#create_initial_media_buy
    error: request_signature_required
    cascade affects:
      - creative_fate_after_cancellation/{cancel_buy, verify_creative_*, reuse_creative_on_new_buy}
      - invalid_transitions/{unknown_media_buy, unknown_package, double_cancel}
      - inventory_list_no_match/no_match_attempt
      - inventory_list_targeting/{create_with_both_lists, verify_create_persisted, update_swap_lists}
      - pending_creatives_to_start/{create_without_creatives, supply_creatives, verify_transition}
      - measurement_terms_rejected/{reject_terms, accept_terms}
```

This makes the prioritization obvious — fix one thing, unblock eleven.

## Why this matters

Most reference-impl debugging happens in tight loops: change → eval → read output → next change. Cascade noise makes "what's the next single thing to fix" hard. Surfacing root cause attribution turns a 5-iteration debugging session into 1-2.

For published AAO Verified scoring it also matters — a reader of a public agent's compliance card wants to know "is this 1 bug or 11"?

## Implementation hint

The runner already tracks step dependencies (`stateful: true` + context_outputs/context_inputs in YAML). When step B's `Skipped` reason is "unresolved context X" and X was defined by step A that failed, A is the root. Same chain across phases.

## References

- Seller eval cycle: <https://github.com/kapoost/purrsonality-seller-agent/commits/main> (commits `053f7b4`, `9bf55cf`, `16d5c65`, `1a1bcba` on 2026-06-19)
