# bragent

[![CI](https://github.com/kapoost/bragent/actions/workflows/ci.yml/badge.svg)](https://github.com/kapoost/bragent/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/kapoost/bragent)](https://github.com/kapoost/bragent/releases/latest)
[![License](https://img.shields.io/badge/license-Apache_2.0-blue.svg)](LICENSE)

Open-source [AdCP](https://adcontextprotocol.org) **Sponsored Intelligence** brand agent. Single Go binary. Self-hosted. No vendor lock-in.

A small business runs `bragent` on its own server and becomes a first-class participant in the AdCP ecosystem — AI assistants (ChatGPT, Claude, Perplexity, …) can discover the brand, browse its offering, and run a sponsored conversation with the user without leaving the assistant.

## Inbound vs. outbound brand discovery

Most "AI marketing" plays today are **outbound**: the brand pays a buyer agent to find users on a publisher. Sponsored Intelligence flips it. The user already trusts an AI assistant, the assistant routes intent to brands the user has signalled affinity for, and the brand answers with its own SI agent — context, product details, in-conversation handoff. **Inbound** discovery.

`bragent` is the inbound side, on the smallest possible footprint:

- One Go binary. Stdlib + two pure-Go deps. No CGO, no container required.
- Runs against a JSON product feed you already publish (or a static file).
- Speaks **MCP over JSON-RPC/HTTP** plus the SI lifecycle: `si_initiate_session` → `si_send_message` → `si_terminate_session`.
- LLM is the only external call and it's pluggable — Ollama for fully local, OpenAI / vLLM / llama.cpp via the same `/v1/chat/completions` shape.

## 30-second quickstart

```sh
# install
go install github.com/kapoost/bragent/cmd/bragent@v0.1.0

# run the full SI lifecycle against ourselves, then exit
bragent --config $(go env GOPATH)/pkg/mod/github.com/kapoost/bragent@v0.1.0/config.example.toml \
        --simulate-host
```

`--simulate-host` is the canonical smoke contract: the binary boots, fires a real `si_initiate_session` against its own listener, logs the wire response, and exits 0. If you see `"session_status":"active"` you have a working SI brand agent.

For a long-running deployment:

```sh
cp config.example.toml config.toml   # edit brand.{name,domain} + feed.url
bragent --config config.toml
# bragent listening listen=:8080 brand="Acme Outdoor" domain=... products=4 llm=mock
```

Then point a buyer agent at `http://your-host/mcp` for MCP/JSON-RPC and `http://your-host/.well-known/{brand,adagents}.json` for discovery.

## Configuration

```toml
[brand]
name   = "Acme Outdoor"
domain = "shop.acme-outdoor.example"

[server]
listen = ":8080"

[feed]
url            = "https://shop.acme-outdoor.example/feed.json"
refresh_period = "10m"

[store]
path = ".cache/bragent.db"

# Optional — switches from deterministic Mock LLM to any
# OpenAI-compatible /v1/chat/completions endpoint.
# [llm]
# endpoint = "http://localhost:11434/v1"   # Ollama
# model    = "llama3.2"
# api_key  = ""                             # required for OpenAI / hosted vLLM
```

Feed `url` accepts `file://` for local fixtures and `http(s)://` for production. JSON is the only format today — array of product objects matching `feed.Product` (see `feeds/example.json`).

## Deployment recipes

### Bare VPS (systemd)

```ini
# /etc/systemd/system/bragent.service
[Unit]
Description=bragent AdCP SI brand agent
After=network.target

[Service]
Type=simple
User=bragent
WorkingDirectory=/var/lib/bragent
ExecStart=/usr/local/bin/bragent --config /etc/bragent/config.toml
Restart=on-failure
RestartSec=5s

# Hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/bragent

[Install]
WantedBy=multi-user.target
```

```sh
sudo install -m 0755 ./bragent /usr/local/bin/bragent
sudo systemctl enable --now bragent
```

### Caddy in front (TLS + brand discovery)

```caddy
shop.acme-outdoor.example {
    encode zstd gzip
    reverse_proxy /mcp                 127.0.0.1:8080
    reverse_proxy /.well-known/*       127.0.0.1:8080
    # everything else stays your existing site
}
```

The `/.well-known/{brand,adagents,healthz}.json` endpoints are unauth by design — buyer agents need them without a token to bootstrap discovery.

### Fly.io

`fly.toml`:

```toml
app = "bragent-acme"
primary_region = "waw"

[build]
image = "ghcr.io/kapoost/bragent:v0.1.0"

[[services]]
internal_port = 8080
protocol      = "tcp"

[[services.ports]]
port     = 443
handlers = ["tls", "http"]
```

Mount config as a secret or bake into a private image. SQLite session store lives on the persistent volume.

## Architecture

```
cmd/bragent/main.go      entrypoint + --simulate-host smoke loop
internal/config/         TOML loader + validation
internal/feed/           JSON feed loader, cache, periodic refresh, snapshot+search
internal/llm/            Provider interface + Mock + OpenAI-compatible HTTP
internal/mcp/            JSON-RPC 2.0 transport, /mcp + /.well-known/* router
internal/si/             capabilities + SI handlers (get_offering, initiate, send_message, terminate)
internal/store/          SQLite sessions + messages, schema-versioned
internal/wellknown/      brand.json + adagents.json renderers
feeds/example.json       Acme Outdoor fixture (4 products)
config.example.toml      file://./feeds/example.json out-of-the-box
.github/workflows/ci.yml go vet + build + --simulate-host assertion
```

## Spec status

`sponsored_intelligence.core` is **experimental** in AdCP 3.0 with at least six weeks' notice on breaking changes. SI types live hand-rolled in `internal/si/types.go` against the published spec. When upstream `adcp-go` ships SI, we revisit.

## Releases

[v0.1.0](https://github.com/kapoost/bragent/releases/tag/v0.1.0) — first tagged release covering the full M1–M4 SI lifecycle.

## References

- AdCP protocol: <https://docs.adcontextprotocol.org>
- Sponsored Intelligence chat protocol: <https://docs.adcontextprotocol.org/docs/sponsored-intelligence>
- Sister project (seller-side AdCP agent): <https://github.com/kapoost/cats>

## License

Apache License 2.0 — see [`LICENSE`](LICENSE).
