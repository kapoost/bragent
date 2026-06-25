# [ARCHIVED 2026-06-25] Comply runner: surface schema-validation error code + JSON pointer in step failure detail

> **Status: ARCHIVED.** This draft was written 2026-06-19 during a debugging session where I lost ~2h tracking down a schema-enum mismatch (`makegood-remedy` enum value). At the time the comply runner surfaced only "step failed" without the validator error detail.
>
> **Why archived:** SDK 9.0.0 (released 2026-06-18) introduced structured runner notices that surface input-schema field stripping and schema validation failures with more detail. See SDK 7.11.6 release notes (`e0cf1e5`) which started the pattern, expanded in 9.0.0.
>
> Kept as historical reference for the AAO compliance debugging archaeology. Do not file as new GH issue — the pattern is already addressed upstream.
>
> Original draft below.

---

**Repo target:** `adcontextprotocol/adcp` (or wherever the comply runner / `@adcp/sdk` storyboard runner lives — happy to file in the right place if redirected)
**Type:** Runner UX / debuggability
**Affects:** Any agent author whose response shape drifts from the bundled JSON schemas

## Summary

When a step's `validations: - check: response_schema` fails, the storyboard runner reports the **step** as failed but does not expose the **validator error** (which field, what enum was expected, what was received). Without that, the symptom is disconnected from the cause and downstream cascade hides the root.

## Concrete repro from a recent debugging cycle

`purrsonality-seller` (Bun + `@adcp/sdk` 7.11.1, target 3.0.18) declared `measurement_terms.makegood_policy.available_remedies = ['makegood_inventory', 'credit_note']` on a product. Local SDK Zod types didn't catch the mismatch. Direct `curl POST /mcp tools/call get_products` returned HTTP 200 with a response that **the runner's schema validator silently rejected** (the spec's `/schemas/3.0.x/enums/makegood-remedy.json` is `additional_delivery | credit | invoice_adjustment`).

Symptom in `evaluate_agent_quality` output:
```
FAILED: media_buy_seller/creative_fate_after_cancellation/setup
  - Discover a product
  - Create the initial media buy: Skipped: unresolved context variables from prior steps: product_id, pricing_option_id.

FAILED: media_buy_seller/inventory_list_no_match/discover
  - Discover products

FAILED: media_buy_seller/refine_products/brief
  - Send a brief

[... + 4 more "Discover a product" failures across UNRELATED storyboards]
```

Seven different storyboards all showed "Discover a product FAILED" with no further detail. The actual cause was a single invalid enum value 3 levels deep in `products[0].measurement_terms.makegood_policy.available_remedies[0]`.

Debugging required:
1. Reading the YAML for each failing storyboard to find the common step (`get_products`)
2. Verifying the agent responds 200 to direct `tools/call`
3. Manually re-validating the response against `/dist/schemas/3.0.12/core/measurement-terms.json` to find the offending field

Total elapsed: ~2 hours. The fix was a one-line config change.

## Ask

When a step has `check: response_schema`, surface in the failure detail:

```yaml
FAILED: media_buy_seller/inventory_list_no_match/discover
  - Discover products
    schema_error: VALIDATION_FAILED
    schema_pointer: "/products/0/measurement_terms/makegood_policy/available_remedies/0"
    expected: "enum ['additional_delivery', 'credit', 'invoice_adjustment']"
    received: "makegood_inventory"
```

Optional improvement: when N storyboards fail at the same step with the same schema error, collapse them in the human-readable summary:

```
FAILED: 7 storyboards failed at get_products/response_schema validation
  Root cause: products[0].measurement_terms.makegood_policy.available_remedies[0] = "makegood_inventory" (not in enum)
  Storyboards affected: creative_fate_after_cancellation/setup, inventory_list_no_match/discover, refine_products/brief, …
```

## Why this matters

Reference implementation authors are the primary audience for the comply suite. Silent schema errors push them into spec archaeology when a one-line pointer would close the gap. The cost-of-debugging tax discourages adoption — every brand-agent / publisher who hits this delay loses 1-2h to a symptom that's already structured data inside the validator.

## Implementation hint

If the runner already collects `ajv` / `zod` / equivalent validator errors internally, just plumb the first error path + expected/received into the `AdvisoryObservation` with `source: storyboard_step`. No new mechanism, just an unmasking of state already present.

## References

- Seller commit fixing the actual issue: <https://github.com/kapoost/purrsonality-seller-agent/commit/16d5c65>
- Spec schema: `/schemas/3.0.x/core/measurement-terms.json` + `/enums/makegood-remedy.json`
