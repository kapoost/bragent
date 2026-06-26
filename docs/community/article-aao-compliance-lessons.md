# Reference implementation, real numbers: lessons from one day of AAO compliance debugging

> A field report on bringing a brand-side sales agent through AAO Verified storyboards.
> Stack: Bun + `@adcp/sdk` 7.11.1, target 3.0.18.
> Agent: `purrsonality-seller` — single-publisher display slot on a cat-personality quiz.
> Outcome: `media_buy` track from 31/49 → 49/49 in four commits.

The AdCP comply storyboard suite is the closest thing the AdCP community has to a reference exam. The shape it asserts — request/response wires, state transitions, error codes — is what makes interoperability tractable. But if you ship a real implementation against it, the suite teaches you things the schemas alone don't.

This post collects five concrete findings from one day of bringing `purrsonality-seller` to a passing `media_buy` track. If you're building a brand-agent, publisher seller, or signal provider for AAO Verified, these are the corners I cut myself on so you don't have to.

## 1. Silent schema-validation cascade via enum mismatch

The bug: my product declared

```ts
measurement_terms: {
  makegood_policy: {
    available_remedies: ['makegood_inventory', 'credit_note'],  // ← these aren't enum values
  },
}
```

The actual `/schemas/3.0.x/enums/makegood-remedy.json` enum is `additional_delivery | credit | invoice_adjustment`. Plausible-sounding strings; not valid.

My local SDK types didn't flag it (the TypeScript signature in `@adcp/sdk` 7.11.1 widens too generously here). Direct `curl POST /mcp tools/call get_products` returned HTTP 200 with the response. **But the AAO comply runner ran `response_schema` validation and silently rejected the entire response.**

What I saw:
```
FAILED: media_buy_seller/inventory_list_no_match/discover     - Discover products
FAILED: media_buy_seller/inventory_list_targeting/discover_product
FAILED: media_buy_seller/refine_products/brief                - Send a brief
FAILED: media_buy_seller/pending_creatives_to_start/setup
FAILED: media_buy_seller/invalid_transitions/setup
FAILED: media_buy_seller/creative_fate_after_cancellation/setup
FAILED: media_buy_seller/measurement_terms_rejected/discover_products
```

Seven different storyboards. All "Discover a product FAILED". No advisory observation. No error code. No JSON pointer to the field.

The symptom (Discover failed) was four function calls away from the cause (a downstream enum in a field most of those storyboards weren't even reading). It took ~2h of spec archaeology — pulling the storyboard YAMLs, finding the common step, manually re-validating my response against the bundled JSON schemas — before the enum mismatch surfaced. The fix was a one-line config change.

**Takeaway for implementers:** if you see N storyboards fail at the same step shape, manually validate your response against the schema bundle in `node_modules/@adcp/sdk/compliance/cache/<version>/`. The runner's silent rejection masks the real error.

**Takeaway for the WG / comply runner:** the validator already has the error path internally. Plumbing it through to the `AdvisoryObservation` surface would save every implementer hours. (Filed as a separate GitHub issue.)

## 2. Signed-requests vs comply: a chicken-and-egg

`purrsonality-seller` declared:

```ts
requestSigningCapability: {
  supported: true,
  required_for: ['create_media_buy', 'update_media_buy'],  // RFC 9421 signature required
  supported_for: ['sync_creatives'],
}
```

This is the production-grade posture: bearer tokens get you discovery and reads, but every mutating buy-lifecycle operation requires an RFC 9421 signature with content-digest, jti dedup, and key rotation.

The AAO comply storyboard runner does not sign requests. So:

```
FAILED: media_buy_seller/creative_fate_after_cancellation/setup
  - Create the initial media buy: MCP error -32603: {"error":"request_signature_required",
    "message":"Operation \"create_media_buy\" requires a signed request"}

[... + 11 more storyboards with the same first-step failure]
```

12 storyboards in the `media_buy_seller` track failed at the first mutating step. No way to pass `media_buy` track in AAO Verified with this posture.

The spec offers no escape hatch. `account.mode === 'sandbox'` is a property of the resolved account, but the signing check is a request-property gate in the SDK verifier — there's no "sandbox accounts bypass signing" provision. The only workaround is to move both ops to `supported_for` (signing accepted but not required) — which is what I did. This shipped **weaker** runtime auth on the reference implementation than I'd want production sellers to adopt.

**This is the perverse incentive structure:** AAO compliance discourages stronger auth. To pass the badge, you ship looser security than you'd otherwise want. (Filed as a separate GitHub issue — three possible directions to break the deadlock.)

The decision record went into the commit message in SDD style:

```
behavior change: signed-requests required_for was ['create_media_buy','update_media_buy'], is now []

WHY: The AAO comply storyboard runner (and our own unsigned e2e specs)
exercise create_media_buy and update_media_buy WITHOUT RFC 9421
signatures. […]

ROADMAP COMMITMENT: M7+ re-introduces per-account `required_for` when
SDK ships custom verifier predicate hook. Live accounts will get
signing required, sandbox stays open for comply runner.
```

If you flip an auth posture for compliance reasons, encode the *why* and the *re-tighten plan* in git history, not just in code.

## 3. Catalog-size = 1 is an implicit test assumption

The third bug surfaced when I tried to satisfy `measurement_terms_rejected/discover_products` ("Find a product that supports measurement_terms") by adding a second product to the catalog. It seemed clean: one standard SKU + one premium SKU declaring `measurement_terms`.

Adding the SKU broke **seven other storyboards** — `inventory_list_no_match/discover`, `inventory_list_targeting/discover_product`, `refine_products/{brief, refine}`, `pending_creatives_to_start/setup`, `creative_fate_after_cancellation/setup`, `invalid_transitions/setup`. Each one pulls `products[0].product_id` from the get_products response and expects extraction to produce a consistent result.

The storyboard YAML doesn't say "single product catalog". It just uses:
```yaml
context_outputs:
  - path: "products[0].product_id"
    key: "product_id"
  - path: "products[0].pricing_options[0].pricing_option_id"
    key: "pricing_option_id"
```

With a brief-keyed catalog and a non-deterministic sort tie-breaker, `products[0]` becomes unstable. The runner extracts whichever product happens to land first.

**Fix:** consolidated `measurement_terms` onto the existing single product instead of adding a second SKU. The capability declaration is what `measurement_terms_rejected/discover_products` actually needs — not a separate product to make the catalog "feel diverse".

**Takeaway:** reference seller catalogs should stay minimal. Add capability declarations to existing products; don't grow the catalog to demonstrate them.

## 4. Cascade failures obscure root cause

When a storyboard's `setup` phase fails, every downstream stateful step in that scenario prints `Skipped: prior stateful step failed`. In the eval JSON these look like distinct failures.

In a 4-iteration debugging cycle:

| Run | media_buy track | Failures shown | Actual root causes |
|---|---|---|---|
| Baseline | 44/49 | 5 | 2 |
| Phase 1 (add 2nd product) | 37/49 | 12 | 1 |
| Phase 1.1 (consolidate product) | 31/49 | 17 (cascade) | 1 |
| Phase 1.2 (relax signing) | **49/49** | 0 | — |

Reading "31 vs 44" between iterations 2 and 3 looks like a regression. Actually fewer scenarios reached the failing step than in iteration 2, but each one that did surfaced the same single root cause (the signing requirement). Once that one root was fixed, eleven scenarios came back at once.

**Takeaway:** when reading eval output, count root causes not failures. If most failures are `Skipped: prior stateful step failed`, the first failure listed is usually the only thing to fix in that pass. The runner could attribute cascades to their root step_id and the picture would be obvious; filed as a separate GitHub issue.

## 5. Cloudflare grey-cloud is the production hosting gotcha

This one's not protocol-related but it's the deployment story for anyone bringing a brand-agent under their own domain instead of `*.fly.dev`.

The plan: `seller.purrsonality.rocketscience.pl` and `signals.purrsonality.rocketscience.pl` as `CNAME` to the Fly apps, with Fly issuing per-hostname LetsEncrypt certs via ACME.

The trap: Cloudflare DNS records default to **Proxied** (orange cloud). In that mode CF terminates TLS at its edge with its own cert and reverse-proxies to Fly as origin-pull. So when `fly certs create` triggers ACME validation, the HTTP-01 / TLS-ALPN challenge from LetsEncrypt hits Cloudflare's edge, not Fly. The cert never validates and `fly certs check` hangs in `awaiting_configuration` indefinitely.

The fix: set Proxy status to **DNS only** (grey cloud). SNI hits Fly directly, ACME completes in 30–120s.

```bash
# After CNAME with DNS only:
fly certs create seller.purrsonality.rocketscience.pl --app purrsonality-seller
# ✓ Certificate created
# ✓ Certificate is verified and active
```

Once the cert is `Issued + verified`, you *can* flip to Proxied if you want Cloudflare's CDN/WAF in front. You'll need SSL/TLS mode `Full (strict)` and you've doubled your cert surface (CF edge cert + Fly origin cert). For most brand-agent demos the grey cloud is the steady state — there's no CDN/WAF need in front of an MCP endpoint.

This belongs in adcp.org "Production deployment recipes" alongside other host combinations (CF Workers, Vercel, bare VPS). It's the kind of one-line tip that saves the next person an hour.

## Closing

Four commits brought `media_buy` from 31/49 to 49/49 and cleared three advisory observations. The exercise was worth more than the badge: each finding above is something the spec or runner couldn't have told me — they're discovered only by carrying a real wire to a real cert under a real domain.

If you're a fellow implementer building under AAO Verified, hit one of these, and want to skip the day of debugging: that's what this post is for. If you're on the comply runner side and want to land the issues that came out of this, three are filed alongside this post in `docs/community/`.

— *Built and debugged on `purrsonality-seller`, registered in AAO members directory as `https://seller.purrsonality.rocketscience.pl/mcp`, type=sales. Eval log on 2026-06-19. Source: <https://github.com/kapoost/purrsonality-seller-agent>.*

---

# Six months later: porting to @adcp/sdk@9.0.0 stable and 3.1-rc.15

> A postscript on the same agent, six months on. Stack: Bun + `@adcp/sdk@9.0.0` stable, target AdCP 3.1-rc.15 (diagnostic).
> Outcome: AAO 3.1-rc Product Discovery **3/3 PASS**, Media Buy **74/75**, Creative **32/33**, Error Handling **10/10 SILENT** — +23 storyboards in a single day across six phases.

3.1 graduates a lot of surface to "preview" but not "stable": canonical formats (dual v1/v2 emission), pricing currency filters, native-in-feed validation, dependency impairment with cardinality, stale-response advisory. None of these were green on the first deploy under 9.0.0. The six findings below are what we cut ourselves on — different corners than Part One, same flavor.

## 6. `transition.from` is a SHOULD that the runner silently treats as MUST

The bug: `media_buy_seller/dependency_impairment/verify_impaired` failed with **no specific assertion error in the eval output** — just the step title. Curl-probe the same agent against the storyboard's wire request and the response satisfies all four named validations: `response_schema` passes (zod-valid against `GetMediaBuysResponseMediaBuySchema`), `media_buys[0].health == "impaired"`, `impairments[0]` present, `field_contains` matches the expected shape. Nothing in the four checks points anywhere.

Adding `transition: { from: "approved", to: "rejected" }` flipped the storyboard from fail to pass in one deploy. The `transition.from` field is optional per `core/impairment.json` (`"required": ["to"]`); spec text says SHOULD include when known. The runner's `impairment.coherence` cross-resource assertion was treating it as effectively required, without surfacing which assertion failed.

**Spoiler (filed [#5707][i5707], folded to parent [#5664][i5664], fix in [PR #5675][p5675]):** the actual mechanism is in `extractImpairmentObservations` — the coherence ledger observes creative status only from `sync_creatives`/`list_creatives` calls on the wire, never from `comply_test_controller.force_creative_status`. The forced rejection never enters the ledger, so `verify_impaired` reads a buy that correctly reports impaired but references a creative the ledger still considers approved. Independent validation by @danyliukmykola in the PR's isolation matrix confirmed: `{to}`-only response + a `list_creatives` re-read passes; our `transition.from` workaround was coincidental — it added a separate handle the ledger could grip on the inverse rule.

**Takeaway:** when a storyboard fails with no nameable assertion, the runner is likely doing a cross-resource invariant check whose details don't make it to the eval surface. Filing an issue with a curl-probed wire shape is the fastest way to get triage; the WG's "surface which assertion failed inside `impairment.coherence`" UX gap is independent of any specific fix and stays a residual ask after #5675 lands.

[i5707]: https://github.com/adcontextprotocol/adcp/issues/5707
[i5664]: https://github.com/adcontextprotocol/adcp/issues/5664
[p5675]: https://github.com/adcontextprotocol/adcp/pull/5675

## 7. AAO eval state is global across the whole session

The runner doesn't reset agent state between storyboards within a single `evaluate_agent_quality` run. Seeded products from `provenance_enforcement` persist into `dependency_impairment`. Creative status overrides from `creative_lifecycle` leak into `native_in_feed`. Persistent task registry rows from `create_media_buy_async` collide with a subsequent identical id from a later scenario.

This bites in three distinct ways:

- **Isolated vs full eval diverges.** Run `tracks: ["media_buy"]` alone: 74/75. Run full eval after `creative` and `products`: 67/75. The same agent, the same code, different evaluation order. We have a half-dozen storyboards whose failure mode is purely cross-storyboard interference, not anything the seller could have done differently within its own boundary.

- **Filter narrowing has to be storyboard-specific.** Our first attempt at `pricing_currency_filter` hid the default catalog whenever any seeded product was present. That broke `dependency_impairment` (which uses the default product via brief discovery) because by the time it ran, `pricing_currency_filter` had already seeded its fixtures. Final gate: hide default catalog only when *the specific marker that pricing_currency_filter's fixture carries* (`signal_targeting_options`) is present — i.e. trust the fixture's own self-identification, not a global "are seeds present" check.

- **Directives must be single-use.** Phase H (`stale_response_advisory`) marks an upstream as unreachable via `force_upstream_unavailable`. Originally we kept the entry until reset; the next storyboard's `get_products` then rode `STALE_RESPONSE` in `errors[]` even though that storyboard's narrative had nothing to do with stale caches. Fix: `consumeUnavailableUpstream` — pop on first use. The storyboard's wire-shape exercise still validates; downstream scenarios get clean responses.

**Takeaway:** isolated-track evals are the only reliable signal for code correctness; full-eval scores tell you about test isolation, not your agent. If the runner ever ships a controller scenario `reset_session` (`comply_test_controller(scenario="reset_session", params={})`), that becomes the standard primitive between storyboards. Until then, every seeding controller method needs to declare which fixture marker uniquely identifies *its* state vs leftover state.

## 8. `expect_error: true` is an envelope-level contract, not a per-creative one

`creative/native_in_feed/validation_failures` exercises four reject paths (title too long, image wrong size, CTA outside the closed enum, custom pixel tracker without name). My first pass returned per-creative `action: 'failed'` with the error code under `creatives[i].errors[]` — symmetric with our existing `PROVENANCE_REQUIRED` path.

Storyboards: still failed. Eval output: each sub-check still listed as failed. No movement on the score.

Reading the storyboard YAML: `expect_error: true` plus `check: error_code` at the step level. The runner is looking at the **envelope's** `adcp_error.code`, not at the response payload's `creatives[i].errors[]`. The semantics differ: a per-creative failure means "this one creative didn't get accepted, others might have"; an envelope-level error means "the entire call failed". `expect_error: true` selects the second contract.

The fix: throw `AdcpError` instead of pushing a failed result. The SDK serializes the throw as the envelope-level `adcp_error`. One commit, three of four sub-checks flipped to pass (the fourth needed a separate fix — see finding 9).

**Takeaway:** look for `expect_error: true` in any storyboard you're trying to satisfy with a rejection. If present, the runner wants the whole call to fail; per-row failures don't count. Per-row failures are the right pattern for batch-tolerant calls (sync N creatives, some succeed, some fail) — a different contract.

## 9. Closed enums in storyboards use UPPERCASE_UNDERSCORE, not Title Case

After the per-creative → envelope fix above, `validation_failures` passed three of four sub-checks. The CTA enum check still failed.

I'd seeded my allowed CTA list as `{'Learn More', 'Shop Now', 'Sign Up', 'Get Started'}` — the standard human-facing labels. The storyboard's "valid" example in `sync_native_creative` (positive control): `cta: { content: "LEARN_MORE" }`. The "invalid" example in `reject_cta_not_in_enum`: `cta: { content: "EXPLORE_MORE" }`.

Same format — uppercase, underscored — both ends. The closed enum is a *symbolic* enum the buyer codes against, not the surface-rendered button text. The mapping from `LEARN_MORE` → "Learn More" happens client-side at render time, not at submission.

Once we corrected to `{'LEARN_MORE', 'SHOP_NOW', 'SIGN_UP', 'GET_STARTED', 'BOOK_NOW', 'DOWNLOAD'}`, both positive and negative tests passed. Bonus regression we hadn't anticipated: our too-strict Title Case list had been rejecting the valid `LEARN_MORE` from `sync_native_creative` too — `creative` track was 30/33 not 31/33 before the fix.

**Takeaway:** if a storyboard's invalid example is uppercase-underscore, the valid one will be too. The closed enum is a buyer-API identifier, not a UI string. Don't humanise the seed list.

## 10. `format_id_refs` must keep their external `agent_url` — no shortcut

`canonical_formats` seeds products whose `format_ids[]` point at the AAO canonical format catalog: `agent_url: "https://creative.adcontextprotocol.org/"`. The storyboard asserts that exact URL round-trips on the response.

Our `getProducts` was passing format ids as plain strings to `buildProduct` with `agentUrl: FORMAT_AGENT_URL` — our own format catalog hostname. The shortcut overwrites per-format `agent_url`s with a single seller-side value. Convenient for single-agent products; wrong for any product carrying federated format references.

Fix: extend `PurrProductConfig` with an optional `format_id_refs?: ReadonlyArray<{ agent_url: string; id: string }>` field. When it's set, pass the structured shape to `buildProduct` (`formats: [{ id, agent_url }]`) *without* the `agentUrl` shortcut. The SDK's `buildProduct` accepts both calling conventions; the single-agent shortcut is opt-in, not a requirement.

```ts
const useFormatRefs = p.format_id_refs && p.format_id_refs.length > 0;
const base = buildProduct({
  id: p.product_id,
  formats: useFormatRefs
    ? p.format_id_refs!.map((f) => ({ id: f.id, agent_url: f.agent_url }))
    : [...p.format_ids],
  ...(useFormatRefs ? {} : { agentUrl: FORMAT_AGENT_URL }),
  // …
});
```

**Takeaway:** "single-agent product" is a useful default that quietly drops federation. When you start exposing canonical formats from external agents (AAO, governance, brand-specific catalogs), drop the shortcut and pass per-format `agent_url` explicitly.

## 11. Multi-package-same-product needs unique `package_id`s — synth allocation table

`media_buy_seller/dependency_impairment_cardinality` creates a media buy with **two packages on the same `product_id`** to exercise per-package impairment scoping. Our `buildPackageResponse` emitted `package_id = \`${orderId}_${productId}\`` — deterministic, collision-free for one-package-per-product, broken the moment the same product appears twice.

The storyboard captures `$context.package_a_id` from `packages[0].package_id` and `$context.package_b_id` from `packages[1].package_id`, then sends an `update_media_buy` that binds different creatives to each. With colliding ids, the second binding overwrote the first; the runner's `field_value` check on `media_buys[0].impairments[0].package_ids[0] == $context.package_a_id` could never pass because there was no such distinct package.

Fix: introduce `MockOrder.synth_packages` — a per-package allocation table populated at `create_media_buy` time. Detect collisions in `product_ids[]`, suffix `_${index}` only when duplicated (`${orderId}_${productId}_0` and `${orderId}_${productId}_1`); preserve the legacy `${orderId}_${productId}` shape for single-occurrence products so storyboards that captured the old format keep round-tripping. `get_media_buys` and `update_media_buy` read from `synth_packages` when present, fall back to `product_ids[]` for legacy state.

Four storyboards unblocked in one commit. The pattern generalises: any time a buyer surface is `Array<Entity>` and your storage is `Record<EntityID, State>`, you've quietly contracted "no duplicates" into the model. Storyboards that exercise the duplicate path will find it.

**Takeaway:** array-shaped APIs with id-keyed storage assume uniqueness without saying so. When a compliance scenario sends two entries with the same id, the seller has to grow an allocation table that distinguishes them. The legacy id format can stay for the non-colliding case; only the duplicates need the suffix.

## 12. Probe production with curl + MCP as a debugging primitive

Across all six findings above, the resolution sequence was the same:

1. Read the storyboard YAML in `node_modules/@adcp/sdk/compliance/cache/<version>/` to find the actual assertions.
2. Read the schema in `@adcp/sdk/dist/lib/types/schemas.generated.js` to confirm our wire shape is valid.
3. Curl-probe the deployed agent with the storyboard's exact sample request, including the `mcp-session-id` header and a bearer matching the `^demo-acme-outdoor-v\d+$` pattern the seller accepts as a compliance runner credential:
   ```bash
   curl -X POST https://seller.purrsonality.rocketscience.pl/mcp \
     -H 'authorization: Bearer demo-acme-outdoor-v1' \
     -H 'content-type: application/json' \
     -H 'accept: application/json, text/event-stream' \
     -H 'mcp-session-id: probe-1' \
     -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_media_buys",…}}'
   ```
4. Compare actual response against storyboard's assertions field-by-field. If the response satisfies every named check and the runner still fails the step, the failure is a cross-resource invariant the eval output doesn't surface — that's an upstream UX gap, not a seller bug.

For #5707 specifically, step 4 produced the curl response shape that bokelley and danyliukmykola could check independently. Without that probe, the issue would have read as "schema-valid, runner fails, no idea why" — half the value the maintainers extracted from it came from the verbatim response in the body.

**Takeaway:** the storyboard YAML is the source of truth for what the runner asserts; the curl probe is the source of truth for what the seller actually emits. Reading both lets you tell apart "seller bug" from "spec/runner gap" before filing the issue — and gives maintainers something to validate against if the issue is real.

## Closing (postscript)

The six months between Part One and this section weren't accidental. AdCP 3.1 takes the surface that 3.0 standardized and stress-tests it with cross-resource invariants, multi-currency capability filters, federation across format agents, and async dependency impairment with cardinality. None of these are subtle additions — each requires real seller-side work — but the failure modes get subtle precisely because the runner has more layers between "your wire" and "your verdict".

The compliance suite is still the closest thing the community has to a reference exam; that hasn't changed. What changed is that the exam now asks questions where the right answer depends on what the runner observes about your wire over a session, not just what your wire said at the moment. Test isolation, named assertion errors, and probe-driven verification become first-class skills.

If you're a fellow implementer hitting any of these, that's what this postscript is for. If you're upstream and want to land the issues that came out of this, [#5707][i5707] and [#5701][i5701] are the open ones; [#5664][i5664] / [PR #5675][p5675] are nearly there.

— *Postscript built and debugged on the same `purrsonality-seller`, eval log on 2026-06-25. Source: <https://github.com/kapoost/purrsonality-seller-agent>. Six phases (D / H / E basic / E cardinality / G / F + bug closure) across 32 commits, all visible in `git log`. The same agent, six months later — same exercise, harder questions.*

[i5701]: https://github.com/adcontextprotocol/adcp/issues/5701
