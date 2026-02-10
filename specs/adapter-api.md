# Tmux Adapter API Spec

A WebSocket service that exposes gastown agents as a programmatic interface. Clients interact with agents — the adapter handles tmux internally.

## Connection

Single WebSocket connection per client:

```
ws://localhost:{PORT}/ws
```

All communication is JSON messages over this one connection. The client sends requests, the server sends responses and pushes events.

## Message Format

Every message has a `type` field. Requests from the client include an `id` for correlation. Responses echo the `id` back.

```json
// client → server (request)
{"id": "1", "type": "list-agents"}

// server → client (response)
{"id": "1", "type": "list-agents", "agents": [...]}

// server → client (event, no id — unsolicited)
{"type": "agent-added", "agent": {...}}
```

---

## Agent Model

An agent represents a live AI coding agent running in a gastown tmux session. Zombie sessions (tmux alive, agent process dead) are not exposed — only actual running agents.

```json
{
  "name": "hq-mayor",
  "role": "mayor",
  "runtime": "claude",
  "rig": null,
  "attached": false,
  "processCommand": "node"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Session name (e.g., `hq-mayor`, `gt-gastown-crew-max`) |
| `role` | string | Agent role: `mayor`, `deacon`, `witness`, `refinery`, `crew`, `polecat` |
| `runtime` | string | Agent runtime: `claude`, `gemini`, `codex`, `cursor`, `auggie`, `amp`, `opencode` |
| `rig` | string? | Rig name for rig-level agents, null for town-level (`hq-*`) |
| `attached` | bool | Whether a human is viewing this session |
| `processCommand` | string | The actual process running (e.g., `node`, `claude`, `gemini`) |

**How these are derived (internal):**
- `name` — tmux session name (filtered to `hq-*` / `gt-*` prefixes)
- `role` — from `GT_ROLE` session env var
- `runtime` — from `GT_AGENT` session env var
- `rig` — from `GT_RIG` session env var
- `attached` — from `#{session_attached}` tmux variable
- `processCommand` — from `#{pane_current_command}` tmux variable
- Zombie filtering — `IsAgentAlive` pattern: check process names against agent registry

---

## Client → Server Requests

### list-agents

Discover all running gastown agents.

```json
{"id": "1", "type": "list-agents"}
```

Response:
```json
{
  "id": "1",
  "type": "list-agents",
  "agents": [
    {"name": "hq-mayor", "role": "mayor", "runtime": "claude", "rig": null, "attached": true, "processCommand": "node"},
    {"name": "hq-deacon", "role": "deacon", "runtime": "claude", "rig": null, "attached": false, "processCommand": "node"},
    {"name": "gt-gastown-crew-max", "role": "crew", "runtime": "gemini", "rig": "gastown", "attached": false, "processCommand": "gemini"}
  ]
}
```

### send-message

Send a text message to an agent. The adapter handles the full NudgeSession sequence internally (literal mode, 500ms debounce, Escape, Enter with retry, SIGWINCH wake).

```json
{"id": "2", "type": "send-message", "agent": "hq-mayor", "message": "please review the PR"}
```

Response (after the full send sequence completes):
```json
{"id": "2", "type": "send-message", "ok": true}
```

Error:
```json
{"id": "2", "type": "send-message", "ok": false, "error": "agent not found"}
```

### get-history

Capture a snapshot of the agent's output.

```json
{"id": "3", "type": "get-history", "agent": "hq-mayor", "lines": 50}
```

`lines` is optional — defaults to 50. Use `"all"` for full scrollback.

Response:
```json
{"id": "3", "type": "get-history", "agent": "hq-mayor", "content": "... captured text ..."}
```

### subscribe

Start streaming an agent's output. The server begins pushing `output` events for this agent.

```json
{"id": "4", "type": "subscribe", "agent": "hq-mayor"}
```

Response:
```json
{"id": "4", "type": "subscribe", "ok": true}
```

After subscribing, the server pushes `output` events (see below).

### unsubscribe

Stop streaming an agent's output.

```json
{"id": "5", "type": "unsubscribe", "agent": "hq-mayor"}
```

Response:
```json
{"id": "5", "type": "unsubscribe", "ok": true}
```

### get-agent

Get details for a single agent.

```json
{"id": "6", "type": "get-agent", "agent": "hq-mayor"}
```

Response:
```json
{"id": "6", "type": "get-agent", "agent": {"name": "hq-mayor", "role": "mayor", "runtime": "claude", "rig": null, "attached": true, "processCommand": "node"}}
```

---

## Server → Client Events

These are pushed without a request. No `id` field.

### agent-added

A new agent has started. This means a real agent process is running — not just a tmux session appearing.

```json
{"type": "agent-added", "agent": {"name": "gt-gastown-crew-max", "role": "crew", "runtime": "gemini", "rig": "gastown", "attached": false, "processCommand": "gemini"}}
```

### agent-removed

An agent has stopped or its session was destroyed.

```json
{"type": "agent-removed", "name": "gt-gastown-crew-max"}
```

### output

Streaming output from a subscribed agent. Only sent after a `subscribe` request.

```json
{"type": "output", "agent": "hq-mayor", "data": "new output bytes here"}
```

---

## Internal Architecture

The adapter is a Go service. Clients see agents. Internally it's all tmux.

```
┌─────────────┐         ┌──────────────────┐         ┌────────────┐
│   Clients   │◄──ws──►│  Tmux Adapter     │◄──────►│ tmux server│
│  (any lang) │         │                  │         │            │
│             │         │  control mode ────────────►│ sessions   │
│             │         │  pipe-pane (per agent) ───►│ panes      │
└─────────────┘         └──────────────────┘         └────────────┘
```

**Control mode connection:**
- One `tmux -C attach -t "adapter-monitor"` connection at startup
- All commands (list, send-keys, capture-pane, show-environment) go through it
- `%sessions-changed` events trigger re-scan for agent-added/agent-removed

**Agent detection:**
- On `%sessions-changed`: list sessions, filter `hq-*`/`gt-*`, read `GT_AGENT` env var, check `IsAgentAlive` (process name matching + descendant walk)
- Diff against known set → emit `agent-added` / `agent-removed`
- Zombie sessions (tmux alive, agent dead) are never exposed to clients

**Output streaming:**
- On `subscribe`: activate `pipe-pane -o` for that session, route bytes to all subscribed WebSocket clients
- On `unsubscribe` (or last client disconnects): deactivate `pipe-pane`

**Send message:**
- Full NudgeSession sequence through control mode: `send-keys -l` → 500ms sleep → `send-keys Escape` → 100ms sleep → `send-keys Enter` (3x retry) → `resize-pane` wake dance
- Per-agent serialization to prevent interleaving

**History capture:**
- `capture-pane -p -t SESSION -S -N` through control mode
