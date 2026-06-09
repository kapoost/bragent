# bragent

Open-source AdCP **Sponsored Intelligence** brand agent. Self-hosted, single binary, no vendor lock-in.

A small business runs this on their own server. It makes them discoverable in the AdCP ecosystem — particularly as a Sponsored Intelligence brand that AI assistants (ChatGPT, Claude, Perplexity) can connect users to without leaving the conversation.

## Status

**Spike — milestone 1.** Implements `get_adcp_capabilities` and `si_get_offering` over MCP/JSON-RPC. Remaining SI tasks (`si_initiate_session`, `si_send_message`, `si_terminate_session`) return method-not-found until the AdCP 3.x spec finalises their schemas.

Sponsored Intelligence is **experimental in AdCP 3.0** (feature id `sponsored_intelligence.core`). Breaking changes between 3.x releases come with at least 6 weeks notice.

## Philosophy

- No unnecessary dependencies (Go std + `BurntSushi/toml` only)
- Self-hosted where it matters — no mandatory cloud services
- Infrastructure-agnostic — runs on any Linux VPS, container, bare metal
- LLM is the only external call, and it is configurable (Ollama, llama.cpp, vLLM, OpenAI — anything OpenAI-compatible)
- Single binary, no runtime dependencies

## Quick start

```sh
go build -o bragent ./cmd/bragent
./bragent --config config.example.toml
```

In another terminal:

```sh
curl -s http://localhost:8080/mcp \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"si_get_offering","params":{"query":"tent","max_results":3}}' \
  | jq .
```

## Configuration

Copy `config.example.toml` to `config.toml` and edit. Required fields: `brand.name`, `brand.domain`, `feed.url`.

Feed URL accepts `file://` for local testing and `http(s)://` for production. JSON is the only format in M1 — array of product objects (see `feeds/example.json`).

## Architecture

```
cmd/bragent/         entry point
internal/config/         TOML loader + validation
internal/feed/           product feed loader, cache, periodic refresh, search
internal/mcp/            JSON-RPC 2.0 over HTTP transport
internal/si/             AdCP Sponsored Intelligence handlers
```

## References

- AdCP 3.0: https://docs.adcontextprotocol.org/docs/reference/whats-new-in-v3
- Sponsored Intelligence chat protocol: https://docs.adcontextprotocol.org/docs/sponsored-intelligence/si-chat-protocol
- adcp-go SDK: https://github.com/adcontextprotocol/adcp-go (no SI types as of v2.0.1 / 2026-05-31)

## License

Apache License 2.0 — see `LICENSE`.
