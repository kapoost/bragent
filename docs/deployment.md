# Deployment

bragent is one Go binary. There is no database to provision, no
service mesh to wire, no container runtime to mandate. The interesting
deployment question is **where the LLM lives** and **how the public
internet reaches your brand agent**, because Sponsored Intelligence
requires buyer agents (Claude, ChatGPT, Perplexity, …) to fetch
`https://your-brand.example/.well-known/brand.json` over real HTTPS.

This document covers three recipes, in order of complexity:

1. **Tailscale Funnel** — fastest path to a public URL, no VPS needed.
2. **Small VPS + Caddy + Tailscale backplane** — cleanest split: VPS
   does TLS and public DNS, the LLM stays on your hardware at home.
3. **All-in-one VPS** — everything on one box, no home dependency.

Each recipe ends with the same operational checklist: where the SQLite
session store lives, how to rotate the admin token, what to monitor.

---

## Recipe 1 — Tailscale Funnel (fastest MVP)

**When this fits.** You already have a Mac or Linux box that runs
24/7, you have Ollama working locally, and you don't want a separate
VPS yet. You're OK with the `*.ts.net` subdomain on `brand.json`.

**What you get.** A public HTTPS URL like
`https://shop-bragent.tailXXXX.ts.net/mcp` that buyer agents can hit.
Free, no extra hops.

**What you give up.** The published `brand.domain` will be
`shop-bragent.tailXXXX.ts.net`, not your real shop domain. Fine for
demos; meh for prod brand presence.

**Steps.**

```sh
# On the machine running bragent + Ollama (call it acme-edge):

# 1. Install Tailscale, log in.
brew install --cask tailscale          # macOS
# or: curl -fsSL https://tailscale.com/install.sh | sh   # Linux
sudo tailscale up --hostname=acme-edge

# 2. Enable HTTPS certs and Funnel in the Tailscale admin console:
#    https://login.tailscale.com/admin/dns      → enable MagicDNS + HTTPS
#    https://login.tailscale.com/admin/acls    → add "funnel" rule for this node

# 3. Expose bragent's port 8080 publicly.
sudo tailscale serve --bg --https=443 http://localhost:8080
sudo tailscale funnel --bg 443
# This makes https://acme-edge.tailXXXX.ts.net:443 → http://localhost:8080

# 4. Point bragent's brand.domain at the Tailscale URL in config.toml:
cat > config.toml <<'EOF'
[brand]
name   = "Acme Outdoor"
domain = "acme-edge.tailXXXX.ts.net"     # ← whatever Tailscale gave you

[server]
listen = "127.0.0.1:8080"

[feed]
url = "file:///srv/bragent/feeds/example.json"
format = "json"
cache_path = "/srv/bragent/.cache/feed.json"
refresh_interval = "30m"

[llm]
endpoint = "http://localhost:11434/v1"
api_key  = ""
model    = "llama3.2"

[store]
path = "/srv/bragent/.cache/bragent.db"

[admin]
enabled = true
token   = "<openssl rand -hex 32 output>"
EOF

# 5. Run bragent.
./bragent --config config.toml
```

**Smoke from anywhere on the public internet:**

```sh
curl https://acme-edge.tailXXXX.ts.net/.well-known/brand.json
curl https://acme-edge.tailXXXX.ts.net/.well-known/adagents.json
```

**Caveats.**

- Funnel free tier has data transfer caps; review them before
  committing prod traffic.
- The cert is issued by Tailscale's CA, trusted by browsers and Go's
  default `http.Client`. Some hardened buyer agents may reject it —
  test against your target host before going live.
- DNS for `*.ts.net` is Tailscale's. You can layer a CNAME from your
  own domain (`shop.acme-outdoor.com` → `acme-edge.tailXXXX.ts.net`)
  but you also need a cert for the CNAME — Tailscale doesn't issue
  certs for arbitrary domains, so this lands you back in Recipe 2.

---

## Recipe 2 — Small VPS + Caddy + Tailscale backplane (recommended for prod)

**When this fits.** You want a real brand domain on `brand.json`
(`shop.acme-outdoor.com`), you have a beefy machine at home (M1/M2
Mac, Ryzen mini-PC) running Ollama, and you want to pay for compute
only on the cheap public-facing layer.

**Architecture.**

```
        Internet
            │
            ▼
   ┌────────────────────┐         tailnet
   │  VPS ($5/mo)       │ ──────────────────►  ┌──────────────────┐
   │  - Caddy (TLS)     │                       │  Home box (M1)   │
   │  - tailscaled      │                       │  - bragent       │
   │  - 1 vCPU, 1GB RAM │                       │  - Ollama llama3 │
   └────────────────────┘                       └──────────────────┘
   shop.acme-outdoor.com                        100.x.y.z (tailnet)
```

Caddy on the VPS terminates TLS for your real domain and forwards
**only** /mcp, /.well-known/*, and /admin/* over Tailscale to the home
box where the LLM and SQLite live. The VPS is stateless and
replaceable.

**Steps.**

### On the home box (call it `acme-home`)

```sh
# 1. Tailscale.
brew install --cask tailscale && sudo tailscale up --hostname=acme-home

# 2. Ollama up + a model pulled.
brew install ollama && brew services start ollama
ollama pull llama3.2          # or qwen2.5:7b for better quality

# 3. bragent. Listen ONLY on the tailnet IP so the public internet
#    can't bypass Caddy.
TS_IP=$(tailscale ip -4)       # e.g. 100.64.12.7
cat > /srv/bragent/config.toml <<EOF
[brand]
name   = "Acme Outdoor"
domain = "shop.acme-outdoor.com"

[server]
listen = "${TS_IP}:8080"

[feed]
url = "file:///srv/bragent/feeds/example.json"
format = "json"
cache_path = "/srv/bragent/.cache/feed.json"

[llm]
endpoint = "http://localhost:11434/v1"
api_key  = ""
model    = "llama3.2"

[store]
path = "/srv/bragent/.cache/bragent.db"

[admin]
enabled = true
token   = "$(openssl rand -hex 32)"
EOF

# 4. Run bragent under your favourite supervisor (launchd, systemd,
#    pm2, whatever — it's just a Go binary).
/srv/bragent/bragent --config /srv/bragent/config.toml
```

### On the VPS (Hetzner CX22 or DO 1GB, ~$5/mo)

```sh
# 1. Tailscale to join the same tailnet, with --ssh off and
#    --advertise-tags so the VPS can route to acme-home only.
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up --hostname=acme-vps

# 2. Caddy. Auto-cert from Let's Encrypt.
sudo apt install caddy

# 3. /etc/caddy/Caddyfile:
sudo tee /etc/caddy/Caddyfile <<'EOF'
shop.acme-outdoor.com {
    # /mcp and well-knowns are public — buyer agents reach them.
    @public {
        path /mcp /.well-known/*
    }
    handle @public {
        reverse_proxy http://acme-home:8080
    }

    # /admin/* is also proxied but the admin token guards it server-side.
    # Optionally restrict by source IP here for belt-and-suspenders:
    #   @adminallow remote_ip 1.2.3.4/32
    handle /admin/* {
        reverse_proxy http://acme-home:8080
    }

    # Everything else: 404. Don't expose the bragent root.
    handle {
        respond 404
    }
}
EOF
sudo systemctl reload caddy
```

### DNS

In your registrar, point `shop.acme-outdoor.com` (A or AAAA) at the
VPS's public IP. Wait for propagation. Caddy will obtain a Let's
Encrypt cert on first request.

### Smoke from the public internet

```sh
curl https://shop.acme-outdoor.com/.well-known/brand.json   # 200 from bragent
curl https://shop.acme-outdoor.com/.well-known/adagents.json # 200
curl -X POST https://shop.acme-outdoor.com/mcp \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"get_adcp_capabilities"}'
```

**Why this is the recommended posture.**

- **Cost.** $5/mo VPS, $0 LLM (your hardware, your electricity).
- **Latency.** Tailscale's WireGuard mesh is ~20-40ms VPS↔home over
  the wider internet — model inference dominates the budget anyway.
- **Privacy.** Conversations never leave your network; the VPS only
  sees encrypted traffic.
- **Disposability.** VPS dies? Spin up a new one in 5 minutes — all
  state is on the home box.

---

## Recipe 3 — All-in-one VPS

**When this fits.** You don't want a home dependency. You're OK
paying for compute that's big enough to run Ollama, or you'll use a
cloud LLM API instead.

**Cheap path: VPS + cloud LLM** (~$5-10/mo VPS + LLM token costs)

```toml
# config.toml on the VPS
[llm]
endpoint = "https://api.groq.com/openai/v1"
api_key  = "gsk_..."
model    = "llama-3.3-70b-versatile"
```

Groq, Together.ai, OpenAI, DeepSeek, Anthropic (via openai-compat
shim) — anything that speaks `/v1/chat/completions` works. See the
provider table in the README for endpoint URLs.

**Self-hosted path: VPS big enough for Ollama** (~$20-50/mo)

| Model | RAM needed | Hetzner SKU |
|---|---|---|
| `llama3.2:3b` | ~4GB | CX22 (4GB) |
| `qwen2.5:7b`  | ~8GB | CX32 (8GB) |
| `mistral-nemo:12b` | ~12GB | CX42 (16GB) |
| `llama3.3:70b` | 40GB+ | requires GPU host (Hetzner GEX, vast.ai) |

Same Caddy front, same TOML — just both `bragent` and `ollama serve`
run on the VPS.

---

## Operational checklist (all recipes)

### Where state lives

- `/srv/bragent/.cache/bragent.db` — SQLite sessions + audit trail.
  Back it up like any other SQLite: `sqlite3 .cache/bragent.db
  ".backup '/srv/backups/bragent-$(date +%F).db'"`.
- `/srv/bragent/.cache/feed.json` — last-good feed snapshot, used on
  refresh failure. Disposable.
- `/srv/bragent/feeds/example.json` — the authoritative product feed
  if you use a `file://` URL. **This is the source of truth for the
  admin UI's CRUD writes** — version-control it.
- `/srv/bragent/.cache/signing.ed25519` — Ed25519 keypair backing
  `verify_brand_claim` signed responses (M6.1). Mode 0600. Rotate by
  deleting and restarting; the new public key is published at
  `/.well-known/jwks.json` automatically.

### Rotating the admin token

```sh
# 1. New token in config.toml:
sed -i "s/^token =.*/token = \"$(openssl rand -hex 32)\"/" config.toml

# 2. Restart bragent (graceful — in-flight requests finish).
launchctl kickstart -k system/bragent      # macOS launchd
# or: systemctl restart bragent             # Linux systemd

# 3. Re-open the admin URL with the new ?token=... once.
```

The HttpOnly cookie inherits the new token after re-auth.

### What to monitor

bragent emits boot log lines that capture every state knob:

```
bragent listening listen=:8080 brand="Acme Outdoor" domain=... products=4 store=... llm=openai:http://localhost:11434/v1 admin=on brand_rights=kid=abc123
```

In Caddy logs you'll see request rates for `/mcp` (buyer agent hits)
vs `/.well-known/*` (discovery probes). High discovery / low /mcp ratio
means buyer agents are indexing you but not conversing — usually a
brand.json or capabilities mismatch.

### Common failure modes

- **`/mcp` returns 502 from Caddy.** bragent is down or unreachable
  over Tailscale. Check `tailscale status` on both ends; check
  `journalctl -u bragent` or launchd logs.
- **Ollama timeouts.** First request after model unload takes 5-30s.
  bragent has a 60s client timeout; if you hit it, the LLM provider
  falls back to a generic message and the SI lifecycle continues.
  Pre-warm with a cron: `curl -s http://localhost:11434/api/generate
  -d '{"model":"llama3.2","prompt":"hi"}' >/dev/null`.
- **brand.json domain mismatch.** If `[brand].domain` in config doesn't
  match the actual hostname buyer agents fetched it from, agents may
  treat the response as a cross-origin claim and ignore it. Keep
  them aligned.

### Going faster: Cloudflare Tunnel as an alternative to Recipe 2

Cloudflare Tunnel replaces the VPS+Caddy hop with `cloudflared`
running on your home box, talking directly to Cloudflare's edge.
Pros: no VPS to maintain, free tier, fast TLS handshake from
anywhere. Cons: Cloudflare sees your traffic in cleartext at the
edge (vs Tailscale-only path in Recipe 2 where the VPS only sees
encrypted tailnet bytes).

```sh
# On the home box
brew install cloudflared
cloudflared tunnel login
cloudflared tunnel create acme-bragent
cloudflared tunnel route dns acme-bragent shop.acme-outdoor.com
# Config in ~/.cloudflared/config.yml:
#   tunnel: acme-bragent
#   credentials-file: ~/.cloudflared/acme-bragent.json
#   ingress:
#     - hostname: shop.acme-outdoor.com
#       service: http://localhost:8080
#     - service: http_status:404
cloudflared tunnel run acme-bragent
```

Pick the one that matches your trust model.
