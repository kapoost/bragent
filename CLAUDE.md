# bragent — Claude Code working memory

Open-source AdCP Sponsored Intelligence brand agent. Single Go binary that a
small business runs on its own server to become discoverable to AI assistants
via SI.

Repo: `github.com/kapoost/bragent` (private; license Apache 2.0 in `LICENSE`).
Local: `/Users/kapoost/adcp/bragent/`.

## Status

| Milestone | Done | Commit | Surface |
|---|---|---|---|
| M1 | ✅ | `9a263f8` | `get_adcp_capabilities`, `si_get_offering`, JSON feed loader, MCP/JSON-RPC over HTTP |
| M2 | ✅ | `f485f95` | `si_initiate_session`, SQLite session store, `--simulate-host` CLI smoke loop |
| M3 | ✅ | `7ad1cbd` | `si_send_message`, `si_terminate_session`, mock LLM, `/.well-known/{brand,adagents}.json` |
| CI | ✅ | `d47280c` | GitHub Actions: `go vet`, build, `--simulate-host` assertions |
| M4 | ✅ | `f6c4e97` | OpenAI-compatible HTTP provider (Ollama/llama.cpp/vLLM/OpenAI), config-gated via `[llm].endpoint`, Mock stays default |
| M5a | ✅ | `923c3d1` | additive AdCP 3.1.x spec sync: `availability_status`, `context`/`ext` pass-through, hyphenated specialism alias |
| M5b | ✅ | `e4b78d1` | embedded `/admin/` UI: catalog CRUD on file:// feed (atomic write) + in-process chat panel; token-gated, off by default |
| M6.1 | ✅ | `01fbb33` | `verify_brand_claim` with Ed25519 JWS-signed responses, `/.well-known/jwks.json` |
| M6.2 | ✅ | `3ce992e` | top-level `paying_principal` URL + `influence_mode` enum on SI responses (zero-th primitive proposed in WG-SI) |
| M6.3 | ✅ | `3a402aa` | conformance pass for AdCP PR #5501: full `sponsored_context` envelope + `sponsored_context_receipt` with JWS-notarised audit trail; admin dual-trail; `--auto-receipt` synthesis in stdio + Python bridges. Tagged as **v0.2.0**. |
| M6.4 | ✅ | _pending_ | public `/demo/` panel — zero-token, in-memory sessions, read-only catalog, BYOK chat (Anthropic/OpenAI/Groq/DeepSeek with endpoint whitelist), wire-view showing live `sponsored_context` + auto-synthesised receipt for the WG-SI audience. Off by default; `BRAGENT_DEMO_ENABLED=true` on Fly. |

Pełen SI lifecycle smoke-tested: `initiate → message → buy-intent → pending_handoff → terminate` z SQLite audit trail.

## Stack constraints — read before adding deps

- **Go 1.22+**, `module github.com/kapoost/bragent`, pinned in `go.mod`.
- **stdlib + 2 deps total:** `github.com/BurntSushi/toml` (config) and `modernc.org/sqlite` (session store). Pure-Go, CGO-free. **Do not** add CGO bindings — single-binary portability across glibc/musl/Alpine is the whole point.
- **Do not pull `adcp-go` SDK.** As of 2026-06-10 it ships nothing for Sponsored Intelligence (no `si_*` handlers, no SI types). When upstream lands SI, revisit; until then SI types are hand-rolled in `internal/si/types.go` based on the spec example flow.
- **LLM is the only external call** and is configurable (`[llm] endpoint` in TOML). M3 ships a deterministic offline `Mock`; real provider lands behind the `llm.Provider` interface in `internal/llm/`.

## Layout

```
cmd/bragent/main.go          entrypoint + --simulate-host
internal/config/             TOML loader + validation
internal/feed/               JSON product feed: load, cache, periodic refresh, snapshot+search
internal/llm/                Provider interface + Mock responder
internal/mcp/                JSON-RPC 2.0 transport, /mcp + /.well-known/* router
internal/si/                 capabilities + 4 SI handlers (get_offering, initiate, send_message, terminate)
internal/store/              SQLite sessions + messages, schema versioned via PRAGMA user_version
internal/wellknown/          brand.json + adagents.json renderers
feeds/example.json           4-product Acme Outdoor fixture
config.example.toml          file://./feeds/example.json out-of-the-box
.github/workflows/ci.yml     vet + build + simulate-host smoke
```

## Working conventions

- **One source of truth per concept.** Skill IDs / capabilities list in `internal/si/handlers.go:capabilities()`; product shape in `internal/feed/feed.go:Product`; SQLite schema in `internal/store/sqlite.go:migrate()` (bump `currentSchemaVersion` for new migrations).
- **Spec-experimental marker.** Sponsored Intelligence is `experimental` in AdCP 3.0 with a 6-week breaking-change notice. Type comments in `internal/si/types.go` flag this. Before changing SI shapes, check `docs.adcontextprotocol.org/docs/sponsored-intelligence` for spec drift.
- **Commit style:** Conventional Commits. `feat(bragent):`, `fix(bragent):`, `ci:`, `docs:`. Co-author trailer for Claude attribution.
- **Push policy:** local commits OK without confirmation; `git push origin main` requires explicit user authorization. Repo is private so blast radius is contained, but log first.
- **Smoke test before push:** `go vet ./... && go build -o bragent ./cmd/bragent && ./bragent --config config.example.toml --simulate-host`. The simulate-host loop is the canonical M1+M2+M3 lifecycle assertion.

## What NOT to do

- Don't add CGO-dependent SQLite drivers (`mattn/go-sqlite3`). modernc.org/sqlite stays.
- Don't pull `adcp-go` until upstream ships SI types.
- Don't hardcode schemas inside handlers — types live in `internal/si/types.go`, schemas drift, handlers shouldn't.
- Don't break the `--simulate-host` contract. Its assertion line in CI is `grep '"session_id":"sess_'` + `'"session_status":"active"'`. If the wire shape changes, update CI in the same commit.
- Don't make `Bearer` auth required for the public well-known endpoints. `/.well-known/{healthz,brand,adagents}.json` are unauth by design — buyer agents need them without a token.

## Useful commands

```sh
# Build + smoke
go build -o bragent ./cmd/bragent
./bragent --config config.example.toml --simulate-host

# Run for development
./bragent --config config.example.toml

# Type check (no build)
go vet ./...

# CI status
gh run list --repo kapoost/bragent --limit 5
```

## Roadmap

Open for next session:
- **Release tag `v0.1.0`** + GitHub Release notes covering the M1–M4 lifecycle.
- **README polish:** narrative ("inbound vs outbound brand discovery"), quickstart with `./bragent --simulate-host` first-30-seconds, deployment recipes (Caddy + Fly + bare VPS).
- **Optional: Docker image** + multi-arch (amd64/arm64) — Apache 2.0 + private repo means we control distribution.

M5 — additive spec sync from 3.1.0-rc.* (none gated on SI graduation; all backward-compatible):
- **`availability_status` enum on `si_get_offering` response** — `available | limited | sold_out | expired | region_restricted | inactive`, both on the `offering` object and each `matching_products[]` item. Source: AdCP `52bd79c` (rc.12). Wire it into `internal/si/types.go` + `internal/si/handlers.go:getOffering`; derive from `feed.Product.Available` (true → `available`, false → `sold_out` as default) with feed-level override later.
- **`context` + `ext` fields on every SI request/response** — additive open-scope. Source: AdCP `b674082`. Add `Context json.RawMessage` + `Ext json.RawMessage` (both `omitempty`) to all SI types in `internal/si/types.go`; handlers ignore for now, just pass through.
- **`context_outputs.offering.offering_id` path** — verify our wire shape against #3981. Likely a no-op for us since we already key handoff URLs on `offering_id`, but confirm against the rc.12 schema bundle.
- **`AdCPSpecialism` enum value** — spec uses `sponsored-intelligence` (hyphenated) as the specialism ID, marked `preview`. Our capabilities() returns `sponsored_intelligence.core` (underscored). Cross-check both forms; may need to emit both for compat.

M6+ — brand-agent surface beyond SI (still upstream, may overlap with our scope):
- **`verify_brand_claim` / `verify_brand_claims`** — new brand-agent tools landed in 3.1.0-rc.*. Four claim types (`subsidiary`, `parent`, `property`, `trademark`), signed responses, asymmetric trust model. Worth a separate milestone after `v0.1.0` — adds a real brand-identity surface that pairs naturally with SI.

Parked (spec dependency / external WG):
- **A2A Agent Card** at `/.well-known/agent.json` — wait until the A2A working group settles SI/agent-card overlap.
- **A2UI / MCP Apps support in SI** — agent-driven UI rendering (AdCP `8b8b63c`). Large surface, wait until spec stabilises.
- **Brand-side `validate_adagents`-style self-check** — wait until AAO ships the brand-agent storyboard suite.
- **SI graduation watch** — SI tools stay `x-status: experimental` through all of 3.1.x with no committed date. Graduation gate (`required_tools` + graded storyboard) is defined but unscheduled. Keep hand-rolled types until upstream signals.

## Related project links

- Sister project (seller-side AdCP agent): `/Users/kapoost/cats` (Astro/CF Pages) + `/Users/kapoost/adcp/agents/seller` (Bun + @adcp/sdk 7.11.1)
- Reference spec: <https://docs.adcontextprotocol.org/docs/sponsored-intelligence>
- Open upstream issue we filed: <https://github.com/adcontextprotocol/adcp/issues/5449>
