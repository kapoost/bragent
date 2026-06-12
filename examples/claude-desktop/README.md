# bragent → Claude Desktop bridge

Connect a running bragent (local or on Fly) to Claude Desktop so the
user can converse with a brand agent through Claude's normal chat UI.

```
[Claude Desktop]  -- stdio MCP -->  [bragent_bridge.py]  -- HTTP AdCP -->  [bragent]
```

bragent speaks AdCP/SI methods (`si_initiate_session` etc.); Claude
Desktop expects Anthropic-flavour MCP (`tools/list`, `tools/call`).
This bridge translates one to the other in ~200 lines of Python.

The bridge exposes four tools that map 1:1 to AdCP SI methods:
`si_get_offering`, `si_initiate_session`, `si_send_message`,
`si_terminate_session`. The active `session_id` is held in the bridge
process, so Claude can call `si_send_message` without juggling
session IDs explicitly.

## Setup

Requires Python 3.10+ and the official MCP SDK + httpx:

```sh
pip install mcp httpx
```

Make the bridge executable (optional convenience):

```sh
chmod +x examples/claude-desktop/bragent_bridge.py
```

Add to your Claude Desktop config — on macOS:
`~/Library/Application Support/Claude/claude_desktop_config.json`

```json
{
  "mcpServers": {
    "acme-brand-agent": {
      "command": "python3",
      "args": ["/absolute/path/to/bragent/examples/claude-desktop/bragent_bridge.py"],
      "env": {
        "BRAGENT_URL": "https://bragent-demo.fly.dev/mcp"
      }
    }
  }
}
```

Restart Claude Desktop. The four `si_*` tools should appear in the
tool picker (look for "acme-brand-agent" in the integration list).

## Trying it

In a Claude chat:

> Find me an ultralight two-person tent under $400.

Claude should call `si_get_offering`. Then:

> Start a brand conversation about the 2-person tent.

`si_initiate_session` fires — note the response includes the
`paying_principal` (who economically funds this agent — M6.2 disclosure
primitive) and the negotiated `influence_mode`. Continue:

> Ask how heavy it is and what to pair it with.

`si_send_message` fires. When you say something like *"Take me to
checkout"*, the response carries `session_status: pending_handoff`
and a `handoff` URL.

## Env vars

| Variable | Default | Purpose |
|---|---|---|
| `BRAGENT_URL` | `https://bragent-demo.fly.dev/mcp` | Upstream bragent endpoint |
| `BRAGENT_SERVER_NAME` | `acme-brand-agent` | MCP server name surfaced to Claude |
| `BRAGENT_HTTP_TIMEOUT` | `30` | Per-request timeout in seconds |

## Limitations of this bridge (vs the native option)

This bridge is a thin Python shim — fine for a demo, but it adds:

- An external Python runtime dependency.
- A second process per Claude conversation.
- Plain HTTP between the bridge and bragent (no signing).

For production, bragent's `--mcp-stdio` mode runs the same protocol
natively from the Go binary — no Python, no extra process, no HTTP
hop. See the main README for that path.
