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
