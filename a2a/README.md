# A2A Plugin for Crush

The A2A (Agent-to-Agent) plugin lets Crush communicate with other AI agents
using the [A2A v1.0 protocol](https://a2a-protocol.org). It uses the official
[a2a-go SDK](https://github.com/a2aproject/a2a-go) and has two halves:

- **Server mode** — Expose Crush as an A2A agent over JSON-RPC 2.0.
- **Client mode** — Give the LLM tools to discover and invoke remote A2A agents.

Both modes can run simultaneously. This plugin is **mutually exclusive** with
the ACP plugin — use the `crush-a2a` build variant instead of `crush-extended`.

---

## Quick Start

### Expose Crush as an A2A server

```bash
CRUSH_A2A_PORT=8200 crush
```

Verify:

```bash
curl http://localhost:8200/.well-known/agent-card.json | jq .name
# "crush"
```

### Call a remote A2A agent from Crush

Add server(s) to your `crush.json`:

```json
{
  "options": {
    "plugins": {
      "a2a": {
        "servers": [
          {"name": "teammate", "url": "http://localhost:8201"}
        ]
      }
    }
  }
}
```

The LLM now has access to `a2a_list_agents`, `a2a_send_message`, `a2a_get_task`,
and `a2a_attach_file` tools.

---

## Server Mode

The `a2a-server` hook starts a JSON-RPC 2.0 server alongside the normal Crush
TUI, implementing the full A2A v1.0 protocol.

### Server Configuration

```json
{
  "options": {
    "plugins": {
      "a2a-server": {
        "port": 8200,
        "agent_name": "crush",
        "description": "Crush AI coding assistant",
        "skills": [
          {
            "id": "code",
            "name": "Code Assistant",
            "description": "Write, review, and debug code",
            "tags": ["coding"]
          }
        ]
      }
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `port` | int | `8200` | TCP port for the HTTP server |
| `agent_name` | string | `"crush"` | Agent name in the agent card |
| `description` | string | `"Crush AI coding assistant..."` | Description in the agent card |
| `skills` | array | 2 default skills | Skills to advertise |

### Environment Variables

| Variable | Overrides | Example |
|----------|-----------|---------|
| `CRUSH_A2A_PORT` | `port` | `CRUSH_A2A_PORT=8201` |
| `CRUSH_A2A_AGENT_NAME` | `agent_name` | `CRUSH_A2A_AGENT_NAME=reviewer` |
| `CRUSH_A2A_DESCRIPTION` | `description` | `CRUSH_A2A_DESCRIPTION="Reviews PRs"` |

### Agent Card

Served at `/.well-known/agent-card.json` with all A2A v1.0 required fields:

- `name`, `version`, `description`
- `supportedInterfaces` (JSON-RPC transport)
- `capabilities` (streaming: true)
- `skills` (configurable)
- `defaultInputModes`, `defaultOutputModes`

### JSON-RPC Methods

| Method | Description |
|--------|-------------|
| `SendMessage` | Send a message, returns a Task with artifacts |
| `SendStreamingMessage` | Stream task status and artifact updates via SSE |
| `GetTask` | Retrieve task status and artifacts by ID |
| `CancelTask` | Cancel an in-progress task |
| `CreateTaskPushNotificationConfig` | Returns error (not supported) |

### Task Lifecycle

```
submitted → working → completed
                 ├──→ failed
                 └──→ canceled
```

---

## Client Mode

### Client Configuration

```json
{
  "options": {
    "plugins": {
      "a2a": {
        "servers": [
          {"name": "local", "url": "http://localhost:8201"},
          {"name": "remote", "url": "https://agent.example.com", "headers": {"Authorization": "Bearer sk-xxx"}},
          {"name": "disabled", "url": "http://localhost:9000", "enabled": false}
        ],
        "default_timeout_seconds": 120
      }
    }
  }
}
```

### LLM Tools

#### `a2a_list_agents`

Discover agents on configured servers.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `server` | string | No | Server name. Uses first if omitted. |

#### `a2a_send_message`

Send a message to a remote A2A agent.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `input` | string | Yes | Text message to send |
| `server` | string | No | Server name. Uses first if omitted. |
| `context_id` | string | No | Context ID for multi-turn conversations |

#### `a2a_get_task`

Get task status and artifacts.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `task_id` | string | Yes | The task ID to retrieve |
| `server` | string | No | Server name. Uses first if omitted. |

#### `a2a_attach_file`

Attach a file as an artifact to the current A2A task response.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `file_path` | string | Yes | Path to the file |
| `name` | string | No | Human-readable artifact name |
| `description` | string | No | Artifact description |
| `media_type` | string | No | MIME type (auto-detected if omitted) |

This tool only works when Crush is processing an incoming A2A request.

---

## Multi-Agent Architecture

### Hub and Spoke

```
                    ┌──────────────┐
                    │ Orchestrator │  (client mode)
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

```bash
CRUSH_A2A_PORT=8200 CRUSH_A2A_AGENT_NAME=coder crush --cwd /path/to/project &
CRUSH_A2A_PORT=8201 CRUSH_A2A_AGENT_NAME=reviewer crush --cwd /path/to/project &
CRUSH_A2A_PORT=8202 CRUSH_A2A_AGENT_NAME=tester crush --cwd /path/to/project &
```

---

## Spec Compliance

This plugin targets 100% compliance with the
[A2A v1.0 spec-torture test suite](https://github.com/aleksclark/spec-torture):

- 5 discovery tests (agent card)
- 5 lifecycle tests (SendMessage, GetTask, CancelTask)
- 3 messaging tests (text parts, context sharing)
- 2 streaming tests (SSE)
- 4 error handling tests (JSON-RPC error codes)
- 1 push notification test (unsupported)

---

## Testing

### Unit tests

```bash
cd a2a && go test -short ./...
```

### Build the A2A distro

```bash
task distro:a2a
```

### E2E tests

```bash
task distro:all
cd a2a && go test -v -tags e2e -run 'A2A'
```

---

## ACP vs A2A

| Feature | ACP | A2A |
|---------|-----|-----|
| Transport | REST + NDJSON streaming | JSON-RPC 2.0 + SSE |
| Discovery | `GET /agents` | `GET /.well-known/agent-card.json` |
| Task model | Runs (sync/async/stream) | Tasks (with artifacts) |
| Artifacts | Message parts | First-class `Artifact` type |
| SDK | Custom implementation | Official `a2a-go/v2` |
| Spec | ACP draft | A2A v1.0 (Google) |
| Build | `crush-extended` (default) | `crush-a2a` (separate) |
