# Try bragent from your Claude Desktop

This directory ships a small Python bridge that lets **any Claude
Desktop user** talk to a brand agent without installing or running
the Go binary. Five-minute setup.

```
[Claude Desktop]  -- stdio MCP -->  [bragent_bridge.py]  -- HTTP AdCP -->  [bragent]
                                                                          (we host this
                                                                           at bragent-demo.fly.dev,
                                                                           or you point it
                                                                           at your own)
```

Why the bridge exists: bragent speaks AdCP/SI methods
(`si_initiate_session` etc.); Claude Desktop expects Anthropic-flavour
MCP (`tools/list`, `tools/call`). `bragent_bridge.py` translates one
to the other in ~200 lines of Python. The active `session_id` is held
in the bridge process, so Claude can call `si_send_message` without
threading the ID through every turn.

The four exposed tools map 1:1 to AdCP SI lifecycle methods:

| Tool | What it does |
|---|---|
| `si_get_offering` | Preview the brand's catalog (price, availability, description) |
| `si_initiate_session` | Open a conversation — returns `paying_principal` (M6.2) + negotiated `influence_mode` |
| `si_send_message` | Continue the conversation — handles `pending_handoff` → checkout URL |
| `si_terminate_session` | Close the session cleanly |

---

## Setup (Claude Desktop, macOS / Windows / Linux)

### 1. Requirements

- Python **3.10+**
- Claude Desktop

### 2. Get the bridge

Either clone the repo, or just download the single file:

```sh
curl -O https://raw.githubusercontent.com/kapoost/bragent/main/examples/claude-desktop/bragent_bridge.py
```

### 3. Install the two Python deps

```sh
pip install mcp httpx
```

### 4. Add it to your Claude Desktop config

Config file location:

- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`
- **Linux:** `~/.config/Claude/claude_desktop_config.json`

Add (or merge into) the `mcpServers` block:

```json
{
  "mcpServers": {
    "acme-brand-agent": {
      "command": "python3",
      "args": ["/absolute/path/to/bragent_bridge.py"],
      "env": {
        "BRAGENT_URL": "https://bragent-demo.fly.dev/mcp"
      }
    }
  }
}
```

> Replace `/absolute/path/to/bragent_bridge.py` with the actual path
> from step 2. **The path must be absolute** — Claude Desktop won't
> resolve `~` or relative paths.
>
> On Windows the command is `python` not `python3`, and the path
> uses backslashes or forward slashes (both work in JSON).

### 5. Restart Claude Desktop

Cmd+Q (or quit fully via menu), then reopen. Claude Desktop only
reads the config on startup.

---

## Try it

In any Claude chat:

> Use `si_initiate_session` to start a brand conversation with intent
> "weekend backpacking trip" and influence_mode "comparison_set".

You'll see the response includes:

- **`paying_principal`** — the URL identifying who economically funds
  this agent's inference. This is the **M6.2 disclosure primitive**:
  in a sponsored-AI conversation, "who pays for the tokens" answers
  "whose interest is being represented." Maps onto FTC material-
  connection doctrine and EU DSA Art. 26.
- **`influence_mode`** — the negotiated stance for how this session's
  outputs may participate in your reasoning chain
  (`presentation_only` | `reasoning_context` | `comparison_set`).
  Default is `presentation_only`. The brand agent echoes the agreed
  mode on every turn so per-turn audit is verifiable.
- **`session_id`** — the bridge remembers this automatically.

Continue:

> Ask how heavy the tent is and what sleeping pad to pair with it
> for autumn.

`si_send_message` fires. The brand agent (running Claude Haiku on the
deployed instance) responds with catalog-grounded answers.

> Take me to checkout — I want to buy both.

The response carries `session_status: pending_handoff` and a
`handoff.url` pointing at the brand's checkout. In a real deployment
the host would render this URL as a CTA.

---

## Pointing the bridge at a different bragent

`BRAGENT_URL` defaults to the public demo at `bragent-demo.fly.dev`.
To point at your own bragent instance, override via the `env` block:

```json
"env": {
  "BRAGENT_URL": "https://your-bragent.example.com/mcp"
}
```

Run multiple bridges side-by-side (one per brand you want to talk to)
by giving each a unique `mcpServers` key:

```json
"mcpServers": {
  "acme-brand-agent":  { "command": "python3", "args": ["..."], "env": { "BRAGENT_URL": "https://acme.example/mcp" } },
  "globex-brand-agent": { "command": "python3", "args": ["..."], "env": { "BRAGENT_URL": "https://globex.example/mcp" } }
}
```

| Variable | Default | Purpose |
|---|---|---|
| `BRAGENT_URL` | `https://bragent-demo.fly.dev/mcp` | Upstream bragent endpoint |
| `BRAGENT_SERVER_NAME` | `acme-brand-agent` | MCP server name surfaced to Claude |
| `BRAGENT_HTTP_TIMEOUT` | `30` | Per-request timeout in seconds (Anthropic latency = <5s typical) |

---

## Troubleshooting

**Tools don't appear after restart.**

Tail the Claude Desktop MCP log:

```sh
tail -f ~/Library/Logs/Claude/mcp-server-acme-brand-agent.log
```

You should see `Server started and connected successfully` and
responses to `tools/list`. If you see `ModuleNotFoundError: mcp`, the
`pip install` ran against a different Python than `python3` resolves
to — try `python3 -m pip install mcp httpx`, or pin a full Python
path in the config's `command` field.

**Calls hang or time out.**

The public `bragent-demo.fly.dev` instance scales to zero when idle
(free tier) — the first call after several minutes takes ~1s to
cold-start the Fly machine. The default `BRAGENT_HTTP_TIMEOUT` of 30s
covers this; if you want to be sure, bump it to 60.

**Want to see what the bridge sees on the wire.**

Set `PYTHONUNBUFFERED=1` and check the bridge's own stderr; it logs
every upstream HTTP call. Claude Desktop captures stderr into the log
file linked above.

---

## What's on the other end

`https://bragent-demo.fly.dev` runs the same `bragent` binary
described in the repo root README. It is configured with a small demo
catalog (Acme Outdoor: tent, sleeping pad, stove, headlamp), Claude
Haiku as the LLM backing, and an SQLite session store with audit
trail enabled. The public surfaces are at:

- <https://bragent-demo.fly.dev/> — landing page with paying_principal badge
- <https://bragent-demo.fly.dev/.well-known/brand.json> — AAO Brand Protocol manifest
- <https://bragent-demo.fly.dev/.well-known/adagents.json> — authorized-agent declaration
- `POST https://bragent-demo.fly.dev/mcp` — JSON-RPC endpoint (all SI methods)

---

## Native path (no Python, single binary)

This bridge is a thin Python shim — fine for a demo or for plugging
Claude Desktop at a remote bragent. If you want zero Python and zero
extra process, build the bragent Go binary and use its
`--mcp-stdio` mode instead. The binary then runs as the MCP server
directly, sharing the same SQLite store, the same LLM backing, the
same audit trail as the HTTP mode — one binary, two wire formats.
See the repo root README for that path.

---

## Context: the WG-SI conversation this implements

The `paying_principal` + `influence_mode` design is bragent's
contribution to the discussion in the AdCP Working Group's
`#wg-campaign-sponsored-intelligence` channel on 2026-06-11 (response
to B. Masse's three-primitive proposal). Pay-flow is positioned as
the zero-th primitive — the economic ground truth — that grounds
Masse's `influence_mode` declaration, `disclosure_obligation`
propagation, and audit trail. Repo:
<https://github.com/kapoost/bragent>.
