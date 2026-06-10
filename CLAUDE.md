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
- **M4:** Real LLM provider behind `llm.Provider` (OpenAI-compatible HTTP — works with Ollama, llama.cpp, vLLM). Config-gated; mock stays the default for offline/CI.
- **Release tag `v0.1.0`** + GitHub Release notes covering the M1–M3 lifecycle.
- **README polish:** narrative ("inbound vs outbound brand discovery"), quickstart with `./bragent --simulate-host` first-30-seconds, deployment recipes (Caddy + Fly + bare VPS).
- **Optional: Docker image** + multi-arch (amd64/arm64) — Apache 2.0 + private repo means we control distribution.

Parked (spec dependency):
- A2A Agent Card surface at `/.well-known/agent.json` — wait until the A2A working group settles the SI/agent-card overlap.
- Brand-side `validate_adagents`-style self-check tool — wait until AAO ships the brand-agent storyboard suite.

## Related project links

- Sister project (seller-side AdCP agent): `/Users/kapoost/cats` (Astro/CF Pages) + `/Users/kapoost/adcp/agents/seller` (Bun + @adcp/sdk 7.11.1)
- Reference spec: <https://docs.adcontextprotocol.org/docs/sponsored-intelligence>
- Open upstream issue we filed: <https://github.com/adcontextprotocol/adcp/issues/5449>
