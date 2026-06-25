# Sellers that require RFC 9421 signed requests cannot pass `media_buy_seller` track in AAO Verified

**Repo target:** `adcontextprotocol/adcp` (protocol-level question)
**Type:** Design / incentive structure
**Status:** Open question — request feedback from comply maintainers + RFC 9421 advocates
**Applies to:** AdCP 3.0 / 3.1.0-rc.14 / @adcp/sdk through 9.0.0 stable (verified 2026-06-25)

## Summary

A seller agent that declares `request_signing_capability.required_for = ['create_media_buy', 'update_media_buy']` (production-grade auth posture) cannot earn the `media_buy` track in AAO Verified (Spec) certification. The AAO comply storyboard runner does not sign requests, so every mutating storyboard hits `401 request_signature_required` at the first `create_media_buy` step.

The current spec has no escape hatch for this: `account.mode === 'sandbox'` is an account property, but the signing check is a request-property gate. There's no provision for "sandbox accounts bypass signing" in the verifier semantics.

This creates a perverse incentive: **AAO compliance discourages stronger auth.**

## Concrete impact

In a recent `purrsonality-seller` debugging cycle:

| Posture | media_buy track |
|---|---|
| `required_for: ['create_media_buy', 'update_media_buy']` | 31/49 — 12 storyboards fail at first mutating step with `request_signature_required` |
| `required_for: []` (signed opt-in only) | **49/49** |

The flip was a 5-line config change in `signing.ts`. It worked, but it ships **weaker** runtime auth on the reference implementation than I'd want production sellers to adopt.

## Three possible directions

### A) Comply runner adds RFC 9421 signing
Runner generates a sandbox-only key pair, signs all requests, and surfaces the kid as part of `comply_test_controller` setup. Sellers add the sandbox kid to their JWKS for the test duration.
- **Pro:** Exercises the signed path; sellers can require signing and still pass.
- **Con:** Significantly expands runner scope; key lifecycle in CI; kid rotation; sellers need a JWKS write API.

### B) Sandbox-mode bypass in signed-requests verifier
The `signed-requests` verifier checks `ctx.account.mode === 'sandbox'` (or equivalent) before enforcing. Live accounts get signing required; sandbox accounts bypass.
- **Pro:** Production sellers can stay strict. Comply runner doesn't need to change.
- **Con:** Verifier predicate needs to be `(ctx) => boolean` not a static list. `@adcp/sdk` 7.11.x verifyApiKey takes `required_for: readonly string[]` — extending to a predicate is an SDK API change. Also, "skipping signing in sandbox" arguably weakens the test surface itself.

### C) Capability-declaration-only signing
`required_for` becomes purely informational (discovery for buyers) and verification stays at the agent's discretion. Sellers that 401 unsigned mutating requests do so via their own code, not via SDK enforcement; comply runner only checks the declaration matches the behavior.
- **Pro:** No spec change needed; runner can probe both signed and unsigned paths separately and report what the agent supports.
- **Con:** Less prescriptive; relies on seller self-implementation correctness.

## What I'd ask the WG

1. Is this trade-off documented anywhere? Did prior implementers hit it and accept the workaround silently?
2. Which of A/B/C aligns with the WG's direction for cryptographic auth in AdCP 3.1.x and beyond?
3. For now: is the recommended posture for **reference implementations** to declare `required_for: []` and let production sellers tighten? Or should reference impls hold the line and accept incomplete AAO Verified status?

## Status quo workaround (what I shipped)

Flipped `required_for: []` on `purrsonality-seller`. Bearer remains primary auth (encrypted at AAO + Fly secrets, TLS-only). Signed-requests stays in `supported_for` as opt-in. Commit message records the decision as `behavior change: required_for was X, is now Y, here's why` with a panel-of-perspectives review in the body.

Roadmap commitment: re-tighten when SDK ships per-account `required_for` predicate (option B).

## SDK 9.0.0 stable context (verified 2026-06-25)

The seller has since been upgraded to `@adcp/sdk@9.0.0` stable. The signed-requests verifier semantics in 9.0.0 still take `required_for: readonly string[]` (no predicate), so option B remains a future API change. 9.0.0 did expand signing scope — `3652403: feat(signing): sign and verify webhooks with the request-signing key` (release notes) — but the comply runner gap on signed mutating requests is unchanged.

The deadlock described above remains accurate on 9.0.0: any seller declaring the `signed-requests` specialism AND configuring `required_for: [<mutating ops>]` fails AAO comply on negative vector 001 (`unsigned + required_for → request_signature_required`) regardless of SDK version. The trade-off between badge eligibility and stronger auth is structural, not version-dependent.

## References

- Seller commit: <https://github.com/kapoost/purrsonality-seller-agent/commit/1a1bcba>
- RFC 9421: HTTP Message Signatures
- Related: existing `comply_test_controller` sandbox gate (account.mode === 'sandbox' is checked for controller dispatch, just not for signing)
