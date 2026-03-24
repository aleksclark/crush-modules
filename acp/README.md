# ACP Plugin for Crush

The ACP (Agent Communication Protocol) plugin lets Crush instances communicate
with each other and with any ACP-compatible agent over HTTP. It has two halves:

- **Server mode** — Expose your Crush instance as an ACP agent that others can call.
- **Client mode** — Give the LLM tools to discover and invoke remote ACP agents.

Both modes can run simultaneously in the same Crush instance.

---

## Table of Contents

- [Quick Start](#quick-start)
- [Server Mode](#server-mode)
  - [Configuration](#server-configuration)
  - [Environment Variables](#environment-variables)
  - [API Endpoints](#api-endpoints)
  - [Run Modes](#run-modes)
  - [Run Lifecycle](#run-lifecycle)
  - [Streaming Events](#streaming-events)
  - [Interacting with the Server](#interacting-with-the-server)
- [Client Mode](#client-mode)
  - [Configuration](#client-configuration)
  - [LLM Tools](#llm-tools)
  - [Multi-Turn Conversations](#multi-turn-conversations)
- [Protocol Reference](#protocol-reference)
  - [Messages](#messages)
  - [Agent Manifest](#agent-manifest)
  - [Runs](#runs)
  - [Errors](#errors)
- [Multi-Agent Architectures](#multi-agent-architectures)
  - [Hub and Spoke](#hub-and-spoke)
  - [Peer-to-Peer](#peer-to-peer)
- [Testing](#testing)
- [Troubleshooting](#troubleshooting)

---

## Quick Start

### Expose Crush as an ACP server (one env var)

```bash
CRUSH_ACP_PORT=8199 crush
```

Crush starts normally and also listens on `http://localhost:8199`. Verify:

```bash
curl http://localhost:8199/ping
# pong

curl http://localhost:8199/agents
# {"agents":[{"name":"crush","description":"Crush AI coding assistant exposed as an ACP agent",...}]}
```

### Call a remote ACP agent from Crush

Add server(s) to your `crush.json`:

```json
{
  "options": {
    "plugins": {
      "acp": {
        "servers": [
          {"name": "teammate", "url": "http://localhost:8200"}
        ]
      }
    }
  }
}
```

The LLM now has access to `acp_list_agents`, `acp_run_agent`, and
`acp_resume_run` tools. It can discover and invoke agents on its own when the
task calls for delegation.

---

## Server Mode

The `acp-server` hook starts an HTTP server alongside the normal Crush TUI,
exposing the running Crush instance as an ACP agent. External clients (other
Crush instances, scripts, or any ACP-compatible client) can submit prompts
and receive responses.

### Server Configuration

Configure via `crush.json` under `options.plugins.acp-server`:

```json
{
  "options": {
    "plugins": {
      "acp-server": {
        "port": 8199,
        "agent_name": "crush",
        "description": "Crush AI coding assistant exposed as an ACP agent"
      }
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `port` | int | `8199` | TCP port for the HTTP server |
| `agent_name` | string | `"crush"` | Agent name returned in the manifest and required in `POST /runs` |
| `description` | string | `"Crush AI coding assistant..."` | Human-readable description in the manifest |

### Environment Variables

Environment variables override JSON config values. This is useful for running
multiple Crush instances on different ports without separate config files.

| Variable | Overrides | Example |
|----------|-----------|---------|
| `CRUSH_ACP_PORT` | `port` | `CRUSH_ACP_PORT=8200` |
| `CRUSH_ACP_AGENT_NAME` | `agent_name` | `CRUSH_ACP_AGENT_NAME=code-reviewer` |
| `CRUSH_ACP_DESCRIPTION` | `description` | `CRUSH_ACP_DESCRIPTION="Reviews pull requests"` |

Invalid port values (non-numeric, zero, negative) are silently ignored.

**Precedence**: env var > JSON config > default value.

### API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/ping` | Health check. Returns `pong`. |
| `GET` | `/agents` | List agents. Returns the single Crush agent manifest. |
| `GET` | `/agents/{name}` | Get a specific agent manifest. 404 if name doesn't match. |
| `POST` | `/runs` | Create a new run (submit a prompt). |
| `GET` | `/runs/{run_id}` | Get the current state of a run. |
| `GET` | `/runs/{run_id}/events` | List all events emitted during a run. |
| `POST` | `/runs/{run_id}/cancel` | Cancel an in-progress run. |

### Run Modes

When creating a run via `POST /runs`, the `mode` field controls how the response
is delivered:

#### Sync (default)

Blocks until the run completes, then returns the final `Run` object.

```bash
curl -X POST http://localhost:8199/runs \
  -H "Content-Type: application/json" \
  -d '{
    "agent_name": "crush",
    "input": [{"role": "user", "parts": [{"content_type": "text/plain", "content": "What files are in src/?"}]}],
    "mode": "sync"
  }'
```

Response: `200 OK` with the completed `Run` JSON.

#### Async

Returns immediately with `202 Accepted` and a `run_id`. The run executes in the
background. Poll `GET /runs/{run_id}` to check status.

```bash
curl -X POST http://localhost:8199/runs \
  -H "Content-Type: application/json" \
  -d '{
    "agent_name": "crush",
    "input": [{"role": "user", "parts": [{"content_type": "text/plain", "content": "Refactor the auth module"}]}],
    "mode": "async"
  }'

# Poll for completion
curl http://localhost:8199/runs/<run_id>
```

#### Stream

Returns an NDJSON (`application/x-ndjson`) stream that emits events in real time
as the run progresses. The stream closes when the run reaches a terminal state.

```bash
curl -N -X POST http://localhost:8199/runs \
  -H "Content-Type: application/json" \
  -d '{
    "agent_name": "crush",
    "input": [{"role": "user", "parts": [{"content_type": "text/plain", "content": "Explain this codebase"}]}],
    "mode": "stream"
  }'
```

The `X-Run-ID` response header contains the run ID.

### Run Lifecycle

```
created ──> in-progress ──> completed
                │
                ├──> failed
                │
                └──> cancelled
```

| Status | Description |
|--------|-------------|
| `created` | Run accepted, not yet started |
| `in-progress` | Crush is processing the prompt (thinking, running tools, etc.) |
| `completed` | Finished successfully. Output messages are populated. |
| `failed` | An error occurred. The `error` field has details. |
| `cancelling` | Cancel requested, shutting down |
| `cancelled` | Run was cancelled before completion |
| `awaiting` | Agent needs additional input (not currently used by the server, but part of the protocol) |

Completed runs are kept in memory for 1 hour, then cleaned up automatically.

### Streaming Events

When using `mode: "stream"`, events are sent as newline-delimited JSON (NDJSON):

```
{"type":"run.created","run":{...}}
{"type":"run.in-progress","run":{...}}
{"type":"message.part","part":{"content_type":"text/plain","content":"Here are the files..."}}
{"type":"message.completed","message":{...}}
{"type":"run.completed","run":{...}}
```

| Event Type | Payload | Description |
|------------|---------|-------------|
| `run.created` | `run` | Run was created |
| `run.in-progress` | `run` | Crush started processing |
| `run.completed` | `run` | Run finished successfully |
| `run.failed` | `run` | Run encountered an error |
| `run.cancelled` | `run` | Run was cancelled |
| `run.awaiting` | `run` | Agent is waiting for more input |
| `message.created` | `message` | A new message started |
| `message.part` | `part` | Incremental content (streaming text) |
| `message.completed` | `message` | Final message with full content |
| `error` | `error` | Protocol-level error |

### Interacting with the Server

#### Submit a prompt and get the response

```bash
curl -s -X POST http://localhost:8199/runs \
  -H "Content-Type: application/json" \
  -d '{
    "agent_name": "crush",
    "input": [{"role": "user", "parts": [{"content_type": "text/plain", "content": "List all Go files in this project"}]}]
  }' | jq '.output[].parts[].content'
```

#### Stream a response

```bash
curl -N -X POST http://localhost:8199/runs \
  -H "Content-Type: application/json" \
  -d '{
    "agent_name": "crush",
    "input": [{"role": "user", "parts": [{"content_type": "text/plain", "content": "Explain the main function"}]}],
    "mode": "stream"
  }'
```

#### Check all events for a completed run

```bash
curl -s http://localhost:8199/runs/<run_id>/events | jq '.events[].type'
```

#### Cancel a running task

```bash
curl -X POST http://localhost:8199/runs/<run_id>/cancel
```

---

## Client Mode

The ACP client gives the LLM three tools for working with remote agents. The
client is **disabled by default** — configure at least one server to enable it.

### Client Configuration

Configure via `crush.json` under `options.plugins.acp`:

```json
{
  "options": {
    "plugins": {
      "acp": {
        "servers": [
          {"name": "local", "url": "http://localhost:8200"},
          {"name": "research", "url": "https://research.internal:8199", "headers": {"Authorization": "Bearer sk-xxx"}},
          {"name": "disabled-agent", "url": "http://localhost:9000", "enabled": false}
        ],
        "default_timeout_seconds": 120,
        "poll_interval_seconds": 2
      }
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `servers` | array | `[]` | List of ACP server endpoints |
| `default_timeout_seconds` | int | `120` | Timeout for sync requests |
| `poll_interval_seconds` | int | `2` | Polling interval for async runs |

#### Server entry fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | (URL used as name) | Friendly name to reference this server in tool calls |
| `url` | string | *required* | Base URL of the ACP server |
| `headers` | object | `{}` | Custom headers sent with every request (e.g. auth tokens) |
| `enabled` | bool | `true` | Set to `false` to skip this server without removing it |

### LLM Tools

When ACP servers are configured, the LLM gets three tools:

#### `acp_list_agents`

Discover agents available on configured ACP servers.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `server` | string | No | Server name. Uses first configured server if omitted. |

Returns a JSON array of agent summaries (name, description, server).

#### `acp_run_agent`

Invoke a remote agent with a text message.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `agent_name` | string | Yes | Name of the agent to invoke |
| `input` | string | Yes | Text message to send |
| `server` | string | No | Server name. Uses first configured server if omitted. |
| `session_id` | string | No | Session ID for multi-turn conversations |

The tool tries streaming mode first, falling back to sync mode automatically.
Returns the agent's text response, or a JSON object with `status: "awaiting"`
and a `run_id` if the agent needs more input.

#### `acp_resume_run`

Resume an agent that entered the `awaiting` state.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `run_id` | string | Yes | The `run_id` returned when the agent entered awaiting state |
| `input` | string | Yes | Text response to provide |
| `server` | string | No | Server name. Uses first configured server if omitted. |

### Multi-Turn Conversations

Use `session_id` to maintain context across multiple calls to the same agent:

1. LLM calls `acp_run_agent(agent_name: "reviewer", input: "Review src/auth.go", session_id: "review-1")`
2. Agent responds with review comments.
3. LLM calls `acp_run_agent(agent_name: "reviewer", input: "Now check the test coverage", session_id: "review-1")`
4. Agent responds with context from the previous exchange.

If `session_id` is omitted, the server uses its current session.

---

## Protocol Reference

### Messages

Messages are the core communication unit. Each message has a role and one or
more typed parts.

```json
{
  "role": "user",
  "parts": [
    {
      "content_type": "text/plain",
      "content": "What does this function do?"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `role` | string | `"user"` or `"agent"` |
| `parts` | array | Content parts (at least one) |
| `created_at` | string | ISO 8601 timestamp (optional) |
| `completed_at` | string | ISO 8601 timestamp (optional) |

#### Message Parts

| Field | Type | Description |
|-------|------|-------------|
| `content_type` | string | MIME type (e.g. `"text/plain"`) |
| `content` | string | Inline content |
| `content_encoding` | string | Encoding (e.g. `"base64"`) for binary content |
| `content_url` | string | URL to fetch content from |
| `name` | string | Optional name for the part |
| `metadata` | object | Arbitrary key-value metadata |

Crush currently accepts and produces `text/plain` content only.

### Agent Manifest

The manifest describes an agent's identity and capabilities:

```json
{
  "name": "crush",
  "description": "Crush AI coding assistant exposed as an ACP agent",
  "input_content_types": ["text/plain"],
  "output_content_types": ["text/plain"],
  "metadata": {
    "framework": "Crush",
    "natural_languages": ["en"],
    "capabilities": [
      {"name": "code", "description": "Write, review, and debug code"},
      {"name": "tools", "description": "Execute tools like file editing, search, and shell commands"}
    ],
    "tags": ["coding", "AI assistant"]
  }
}
```

### Runs

A run represents a single prompt-to-response cycle:

```json
{
  "agent_name": "crush",
  "run_id": "550e8400-e29b-41d4-a716-446655440000",
  "session_id": "abc-123",
  "status": "completed",
  "output": [
    {
      "role": "agent",
      "parts": [{"content_type": "text/plain", "content": "Here are the files..."}]
    }
  ],
  "created_at": "2025-03-16T10:00:00Z",
  "finished_at": "2025-03-16T10:00:05Z"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `agent_name` | string | Name of the agent |
| `run_id` | string | UUID for this run |
| `session_id` | string | Session identifier |
| `status` | string | Current status (see [Run Lifecycle](#run-lifecycle)) |
| `output` | array | Response messages (populated on completion) |
| `await_request` | object | What the agent needs (when status is `awaiting`) |
| `error` | object | Error details (when status is `failed`) |
| `created_at` | string | When the run was created |
| `finished_at` | string | When the run reached a terminal state |

### Errors

```json
{
  "code": 404,
  "message": "agent \"unknown\" not found"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `code` | int | HTTP status code (optional) |
| `message` | string | Human-readable error description |
| `data` | any | Additional error context (optional) |

---

## Multi-Agent Architectures

### Hub and Spoke

One "orchestrator" Crush instance with ACP client tools configured to call
multiple specialized Crush instances running as ACP servers:

```
                    ┌──────────────┐
                    │ Orchestrator │  (client mode)
                    │   :8199      │
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
       ┌──────▼─────┐ ┌───▼────────┐ ┌─▼───────────┐
       │ Code Writer │ │  Reviewer  │ │  Test Runner │
       │   :8200     │ │   :8201    │ │    :8202     │
       └─────────────┘ └────────────┘ └──────────────┘
       (server mode)   (server mode)   (server mode)
```

**Orchestrator `crush.json`:**

```json
{
  "options": {
    "plugins": {
      "acp": {
        "servers": [
          {"name": "coder", "url": "http://localhost:8200"},
          {"name": "reviewer", "url": "http://localhost:8201"},
          {"name": "tester", "url": "http://localhost:8202"}
        ]
      }
    }
  }
}
```

**Each worker Crush instance:**

```bash
CRUSH_ACP_PORT=8200 CRUSH_ACP_AGENT_NAME=coder crush --cwd /path/to/project
CRUSH_ACP_PORT=8201 CRUSH_ACP_AGENT_NAME=reviewer crush --cwd /path/to/project
CRUSH_ACP_PORT=8202 CRUSH_ACP_AGENT_NAME=tester crush --cwd /path/to/project
```

### Peer-to-Peer

Every Crush instance runs both server and client, so any agent can call any other:

```bash
# Instance A — both server and client
CRUSH_ACP_PORT=8200 CRUSH_ACP_AGENT_NAME=alice crush

# Instance B — both server and client
CRUSH_ACP_PORT=8201 CRUSH_ACP_AGENT_NAME=bob crush
```

Each instance's `crush.json` lists the other as a server:

```json
{
  "options": {
    "plugins": {
      "acp-server": {"port": 8200, "agent_name": "alice"},
      "acp": {
        "servers": [
          {"name": "bob", "url": "http://localhost:8201"}
        ]
      }
    }
  }
}
```

---

## Testing

### Unit tests

```bash
cd acp && go test -short ./...
```

### E2E tests

E2E tests require a built binary with the ACP plugin:

```bash
task distro      # or: task distro:acp
task test:e2e
```

### Integration test (tic-tac-toe)

A full integration test spins up two Crush ACP servers with mock LLMs and plays
a 5×5 tic-tac-toe game between them:

```bash
task distro:all
bash acp/scripts/tictactoe_test.sh
```

The game master sends `POST /runs` to each player alternately. Each player's
mock LLM reads the board, places a mark via the `bash` tool, and responds.
The test validates board integrity and correct mark counts.

---

## Troubleshooting

### Server isn't starting

- Check that the port isn't already in use: `lsof -i :8199`
- Look for the `ACP server started` log message in Crush's output.
- Verify the plugin is registered: `crush --list-plugins | grep acp-server`

### Client tools aren't appearing

The three `acp_*` tools only register when at least one server is configured
and enabled in `options.plugins.acp.servers`. Check your `crush.json`.

### Connection refused from client

- Verify the remote server is up: `curl http://<host>:<port>/ping`
- Check that `agent_name` in `POST /runs` matches the server's configured name.
- If using custom headers (auth), verify they're correct in the server config.

### Run hangs or times out

- Sync mode blocks until the agent finishes. For long tasks, use `async` or
  `stream` mode instead.
- The client has a default 120-second timeout (`default_timeout_seconds`).
  Increase it for expensive prompts.
- Cancel stuck runs with `POST /runs/{run_id}/cancel`.

### Port conflicts when running multiple instances

Use unique ports for each instance via env vars:

```bash
CRUSH_ACP_PORT=8200 crush &
CRUSH_ACP_PORT=8201 crush &
CRUSH_ACP_PORT=8202 crush &
```
