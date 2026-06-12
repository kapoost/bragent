#!/usr/bin/env python3
"""
bragent → Claude Desktop bridge.

Claude Desktop speaks Anthropic MCP over stdio; bragent speaks AdCP
(Sponsored Intelligence) over HTTP. This bridge sits between them:

    [Claude Desktop]  -- stdio MCP -->  [this script]  -- HTTP AdCP -->  [bragent]

It exposes four tools that map 1:1 to AdCP's SI methods. The active
SI session_id is held in this process so a conversation flows
naturally across multiple Claude tool calls without the model having
to thread session IDs explicitly.

Run as a Claude Desktop MCP server — see README.md in this directory
for the claude_desktop_config.json snippet.

Requires: pip install mcp httpx
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
from typing import Any

import httpx
from mcp.server import Server, NotificationOptions
from mcp.server.models import InitializationOptions
import mcp.server.stdio
import mcp.types as types

BRAGENT_URL = os.getenv("BRAGENT_URL", "https://bragent-demo.fly.dev/mcp")
SERVER_NAME = os.getenv("BRAGENT_SERVER_NAME", "acme-brand-agent")
HTTP_TIMEOUT = float(os.getenv("BRAGENT_HTTP_TIMEOUT", "30"))

# stderr-only logging so we don't pollute the stdio JSON-RPC stream.
logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
log = logging.getLogger("bragent-bridge")

app = Server(SERVER_NAME)

# Session state is per-process. Claude Desktop launches one bridge per
# configured MCP server, so this holds the active SI session for the
# user's current conversation. A `si_terminate_session` clears it; a
# second `si_initiate_session` replaces it.
_state: dict[str, str | None] = {"session_id": None}


async def call_bragent(method: str, params: dict[str, Any]) -> dict[str, Any]:
    """Single JSON-RPC POST to bragent's /mcp endpoint. Returns the
    `result` block on success, raises with the wire error message on
    failure so the MCP tool surface gets a clean error."""
    payload = {"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
    async with httpx.AsyncClient(timeout=HTTP_TIMEOUT) as client:
        resp = await client.post(BRAGENT_URL, json=payload)
    resp.raise_for_status()
    body = resp.json()
    if "error" in body and body["error"]:
        err = body["error"]
        raise RuntimeError(f"bragent error {err.get('code')}: {err.get('message')}")
    return body.get("result", {})


@app.list_tools()
async def list_tools() -> list[types.Tool]:
    return [
        types.Tool(
            name="si_get_offering",
            description=(
                "Preview the brand's catalog. Returns matching offerings "
                "with title, description, price, availability. Use this "
                "before si_initiate_session to surface what the brand has."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "query": {"type": "string", "description": "Search query, e.g. 'ultralight 2-person tent'"},
                    "max_results": {"type": "integer", "default": 5},
                },
            },
        ),
        types.Tool(
            name="si_initiate_session",
            description=(
                "Open a brand-agent conversation. Returns a session_id "
                "(automatically remembered for the session by this bridge), "
                "the agent's welcome message, the paying_principal (who "
                "funds this agent), and the negotiated influence_mode. "
                "Call once at the start of a brand interaction."
            ),
            inputSchema={
                "type": "object",
                "required": ["intent"],
                "properties": {
                    "intent": {"type": "string", "description": "User's natural-language intent"},
                    "offering_id": {"type": "string", "description": "Optional — specific product the user is interested in"},
                    "influence_mode": {
                        "type": "string",
                        "enum": ["presentation_only", "reasoning_context", "comparison_set"],
                        "default": "presentation_only",
                        "description": "How this session's outputs may participate in the host's reasoning (M6.2)",
                    },
                },
            },
        ),
        types.Tool(
            name="si_send_message",
            description=(
                "Continue the active brand-agent conversation. Uses the "
                "session_id remembered from si_initiate_session. Returns "
                "the brand's reply, session_status, and a handoff URL "
                "when the brand signals pending_handoff."
            ),
            inputSchema={
                "type": "object",
                "required": ["message"],
                "properties": {
                    "message": {"type": "string", "description": "The user's turn for the brand agent"},
                },
            },
        ),
        types.Tool(
            name="si_terminate_session",
            description=(
                "End the active brand-agent conversation. Forgets the "
                "session_id so the next si_initiate_session starts fresh."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "reason": {"type": "string", "default": "user_exit"},
                },
            },
        ),
    ]


@app.call_tool()
async def call_tool(name: str, arguments: dict[str, Any]) -> list[types.TextContent]:
    try:
        result = await dispatch(name, arguments)
    except Exception as exc:
        log.exception("tool %s failed", name)
        return [types.TextContent(type="text", text=f"Error: {exc}")]
    return [types.TextContent(type="text", text=json.dumps(result, indent=2, ensure_ascii=False))]


async def dispatch(name: str, args: dict[str, Any]) -> dict[str, Any]:
    if name == "si_get_offering":
        return await call_bragent("si_get_offering", {
            "query": args.get("query", ""),
            "max_results": args.get("max_results", 5),
        })

    if name == "si_initiate_session":
        params: dict[str, Any] = {
            "intent": args["intent"],
            "influence_mode": args.get("influence_mode", "presentation_only"),
            "identity": {
                "consent_granted": True,
                "user_pseudo_id": "claude-desktop-bridge",
                "user_language": "en",
            },
        }
        if "offering_id" in args:
            params["offering_id"] = args["offering_id"]
        result = await call_bragent("si_initiate_session", params)
        _state["session_id"] = result.get("session_id")
        log.info("session opened: %s", _state["session_id"])
        return result

    if name == "si_send_message":
        sid = _state["session_id"]
        if not sid:
            raise RuntimeError("no active session — call si_initiate_session first")
        result = await call_bragent("si_send_message", {
            "session_id": sid,
            "message": args["message"],
        })
        if result.get("session_status") == "terminated":
            _state["session_id"] = None
        return result

    if name == "si_terminate_session":
        sid = _state["session_id"]
        if not sid:
            return {"status": "no active session"}
        result = await call_bragent("si_terminate_session", {
            "session_id": sid,
            "reason": args.get("reason", "user_exit"),
        })
        _state["session_id"] = None
        return result

    raise RuntimeError(f"unknown tool: {name}")


async def main() -> None:
    log.info("bragent bridge starting — upstream=%s", BRAGENT_URL)
    async with mcp.server.stdio.stdio_server() as (read_stream, write_stream):
        await app.run(
            read_stream,
            write_stream,
            InitializationOptions(
                server_name=SERVER_NAME,
                server_version="0.1.0",
                capabilities=app.get_capabilities(
                    notification_options=NotificationOptions(),
                    experimental_capabilities={},
                ),
            ),
        )


if __name__ == "__main__":
    asyncio.run(main())
