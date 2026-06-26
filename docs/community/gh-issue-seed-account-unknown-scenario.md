# `comply_test_controller(scenario=seed_account)` returns UNKNOWN_SCENARIO — blocks pagination_integrity_list_accounts for every implementer

**Repo target:** `adcontextprotocol/adcp` (SDK + compliance suite)
**Type:** Bug / SDK gap
**Status:** Reproducible against `@adcp/sdk@9.0.0` stable + `purrsonality-seller` reference impl, against AAO compliance target 3.1-rc (resolved 3.1.0-rc.15)

## Summary

`universal/pagination-integrity-list-accounts.yaml` seeds three sandbox accounts via `comply_test_controller(scenario="seed_account", params={account_id, fixture})` before walking pagination. Every direct-controller call returns:

```json
{
  "status": "failed",
  "success": false,
  "error": "UNKNOWN_SCENARIO",
  "error_detail": "Unrecognized scenario name"
}
```

…regardless of whether the implementer wires a `seed.account` adapter in `ComplyControllerConfig`. The SDK 9.0.0 typed `seed` block exposes `product`, `pricing_option`, `creative`, `plan`, `media_buy`, `creative_format`, `measurement_catalog`, and `buyer_agent` — but not `account`. The dispatcher's scenario→adapter routing appears to enumerate the typed block, so any `seed.account` extension widened in via type intersection is dispatched as UNKNOWN_SCENARIO.

Both storyboards in the file fail at step 1:

```
FAILED: pagination_integrity_list_accounts/seed_accounts
  - Seed first sandbox account: Task failed
  - Seed second sandbox account: Skipped: prior stateful step failed.
  - Seed third sandbox account: Skipped: prior stateful step failed.
FAILED: pagination_integrity_list_accounts/pagination_walk
  - Request the first page with max_results=2: Skipped: prior stateful step failed.
  - Follow the cursor to the next page: Skipped: prior stateful step failed.
```

## Reproduction

Bare-minimum reference impl reproduces:

```ts
import type { ComplyControllerConfig } from '@adcp/sdk/testing';

// Widen the type to allow account in seed
type Cfg = ComplyControllerConfig & {
  seed?: ComplyControllerConfig['seed'] & {
    account?: (
      params: { account_id: string; fixture?: Record<string, unknown> },
      ctx: { input: Record<string, unknown> },
    ) => Promise<void>;
  };
};

export const complyTest: Cfg = {
  seed: {
    product: async (p) => { /* … */ },
    account: async (p) => { /* never called */ },
  },
};
```

Direct curl probe against the deployed agent:

```bash
curl -X POST https://seller.purrsonality.rocketscience.pl/mcp \
  -H 'authorization: Bearer demo-acme-outdoor-v1' \
  -H 'content-type: application/json' \
  -H 'accept: application/json, text/event-stream' \
  -H 'mcp-session-id: probe-1' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"comply_test_controller","arguments":{"account":{"sandbox":true,"account_id":"acct_test_1"},"scenario":"seed_account","params":{"account_id":"acct_test_1","fixture":{"brand":{"domain":"test.example"}}},"context":{"correlation_id":"probe"}}}}'
```

Response (verbatim):

```
{"result":{"content":[{"type":"text","text":"Controller error: UNKNOWN_SCENARIO"}],"structuredContent":{"status":"failed","success":false,"error":"UNKNOWN_SCENARIO","error_detail":"Unrecognized scenario name","context":{"correlation_id":"probe"}},"isError":true}}
```

## Impact

- **Two storyboards in `universal/pagination-integrity-list-accounts.yaml`** unreachable for every seller until SDK adds `seed.account`. That's 5 storyboard steps (3 seeds + 2 pagination walks) graded as `failed`/`skipped` for every implementer.
- The storyboard's `requires_capability` block is empty; without an explicit gate, the runner runs the seed step and fails closed.
- Implementers can't `not_applicable` their way out — there's no advertised capability to disable.

## Proposed fixes

**Option 1 — expose `seed.account` in SDK 9.x typed config (preferred):**

```ts
seed?: {
  product?: SeedAdapter<SeedProductParams>;
  pricing_option?: SeedAdapter<SeedPricingOptionParams>;
  creative?: SeedAdapter<SeedCreativeParams>;
  plan?: SeedAdapter<SeedPlanParams>;
  media_buy?: SeedAdapter<SeedMediaBuyParams>;
  creative_format?: SeedAdapter<SeedCreativeFormatParams>;
  measurement_catalog?: SeedAdapter<SeedMeasurementCatalogParams>;
  buyer_agent?: SeedAdapter<SeedBuyerAgentParams>;
  account?: SeedAdapter<SeedAccountParams>;  // ← add
};

interface SeedAccountParams {
  account_id: string;
  fixture?: Record<string, unknown>;
}
```

Update the dispatcher to route `seed_account` → `seed.account` symmetrically with how `seed_product` → `seed.product` works today.

**Option 2 — `requires_capability` gate on the storyboard:**

If account-list support is expected to remain optional, gate the storyboard the way `dependency_impairment` is being gated in PR #5675:

```yaml
requires_capability:
  path: account.list_supports_seed
  equals: true
```

Sellers that don't expose multi-account list grade `not_applicable`. Less ideal than fixing the SDK because compliant sellers (Option 1) lose coverage of the pagination invariant.

## What I'd ask the WG

1. Is `seed_account` intentionally omitted from the 9.x typed config, or is this an oversight from when `seed.account` was a 3.0-era controller scenario? If intentional, what's the migration path for sellers that need account-list pagination coverage?
2. If Option 1 is the right path, I'm happy to PR the SDK type widening + dispatcher route once direction is confirmed.

## References

- Storyboard: `universal/pagination-integrity-list-accounts.yaml` (cache 3.1.0)
- SDK type: `@adcp/sdk@9.0.0/lib/testing/comply-controller.d.ts` — `seed` interface
- Seller commit attempting the workaround (type intersection): <https://github.com/kapoost/purrsonality-seller-agent/commit/b2bfc38>
- Companion: related runner-side gap on `delivery_monitoring` opaque failures (separate issue if useful)
