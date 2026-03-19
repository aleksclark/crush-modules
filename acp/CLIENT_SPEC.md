# ACP Client Implementation Guide

This document specifies how to build a client that orchestrates Crush agents
running in ephemeral pods with persistent, crash-recoverable sessions.

## Overview

The Crush ACP server exposes a REST+SSE API for agent invocation with session
persistence. A client (typically an orchestrator like Tempotown) manages the
lifecycle:

1. **Spawn** an ephemeral Crush agent (pod/container)
2. **Import** prior session state if resuming
3. **Run** prompts against a stable session ID
4. **Collect** streamed session updates for crash recovery
5. **Export** session state before teardown (or reconstruct from streamed events)

## Base URL

Default: `http://<host>:8199`

Configurable via `CRUSH_ACP_PORT` env var or `crush.json`:

```json
{ "options": { "plugins": { "acp-server": { "port": 8199 } } } }
```

## Authentication

None currently. The server binds to all interfaces. For production, place
behind a service mesh or network policy.

---

## API Reference

### Agent Discovery

#### `GET /agents`

Returns available agents (always one for Crush).

```
GET /agents
```

```json
{
  "agents": [
    {
      "name": "crush",
      "description": "Crush AI coding assistant exposed as an ACP agent",
      "input_content_types": ["text/plain"],
      "output_content_types": ["text/plain"],
      "metadata": {
        "framework": "Crush",
        "capabilities": [
          {"name": "code", "description": "Write, review, and debug code"},
          {"name": "tools", "description": "Execute tools like file editing, search, and shell commands"},
          {"name": "sessions", "description": "Persistent sessions with export/import for crash recovery"}
        ]
      }
    }
  ]
}
```

#### `GET /agents/{name}`

Returns a single agent manifest. 404 if not found.

---

### Runs

#### `POST /runs`

Submit a prompt. This is the primary interaction endpoint.

**Request:**

```json
{
  "agent_name": "crush",
  "input": [
    {
      "role": "user",
      "parts": [{ "content_type": "text/plain", "content": "Fix the login bug in auth.go" }]
    }
  ],
  "session_id": "ses_abc123",
  "mode": "stream"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `agent_name` | string | yes | Must match the agent name (default: `"crush"`) |
| `input` | Message[] | yes | Array of messages with `role` and `parts` |
| `session_id` | string | no | Stable session ID for multi-turn. Omit for isolated one-shot runs |
| `mode` | string | no | `"sync"` (default), `"async"`, or `"stream"` |

**Session behavior:**

- **With `session_id`**: The prompt is appended to the existing Crush session. The LLM sees all prior messages as context. If the session doesn't exist locally, one is created with that ID.
- **Without `session_id`**: A fresh isolated session is created. No conversation history.

**Modes:**

| Mode | Response | Use Case |
|------|----------|----------|
| `sync` | Blocks until complete, returns final `Run` | Simple scripts, testing |
| `async` | Returns `202 Accepted` with `Run` immediately | Fire-and-forget, poll later |
| `stream` | SSE event stream until terminal state | Production — crash recovery, real-time UI |

#### `GET /runs/{run_id}`

Returns the current state of a run.

#### `GET /runs/{run_id}/events`

Returns all events emitted for a run (for replaying after reconnect).

#### `POST /runs/{run_id}/cancel`

Cancels an in-progress run. Returns `202` with the updated run.

---

### Sessions

#### `GET /sessions/{session_id}/export`

Export a complete session snapshot. Returns the full conversation history
including all message parts (text, reasoning, tool calls, tool results).

**Response:**

```json
{
  "version": 1,
  "session": {
    "id": "ses_abc123",
    "title": "Fix login bug",
    "summary_message_id": "",
    "message_count": 12,
    "prompt_tokens": 45000,
    "completion_tokens": 8000,
    "cost": 0.23,
    "created_at": 1710700000,
    "updated_at": 1710700120
  },
  "messages": [
    {
      "id": "msg_001",
      "session_id": "ses_abc123",
      "role": "user",
      "parts": "[{\"type\":\"text\",\"data\":{\"text\":\"Fix the login bug\"}}]",
      "model": "",
      "provider": "",
      "is_summary_message": false,
      "created_at": 1710700000,
      "updated_at": 1710700000
    },
    {
      "id": "msg_002",
      "session_id": "ses_abc123",
      "role": "assistant",
      "parts": "[{\"type\":\"text\",\"data\":{\"text\":\"I'll fix the login bug...\"}},{\"type\":\"tool_call\",\"data\":{\"id\":\"tc_1\",\"name\":\"edit\",\"input\":\"{...}\",\"finished\":true}}]",
      "model": "claude-opus-4",
      "provider": "bedrock",
      "created_at": 1710700010,
      "updated_at": 1710700015
    }
  ]
}
```

**Key details about the `parts` field:**

The `parts` field is a JSON-encoded string (not a nested object) containing an
array of typed parts. Each part has `{"type": "<type>", "data": {...}}` format.

| Part Type | Data Fields | Description |
|-----------|-------------|-------------|
| `text` | `text` | Plain text content |
| `reasoning` | `thinking`, `signature`, `started_at`, `finished_at` | Extended thinking/reasoning |
| `tool_call` | `id`, `name`, `input`, `finished` | Tool invocation |
| `tool_result` | `tool_call_id`, `name`, `content`, `is_error` | Tool execution result |
| `finish` | `reason`, `time`, `message` | Turn completion marker |
| `image_url` | `url`, `detail` | Image attachment |
| `binary` | `Path`, `MIMEType`, `Data` | Binary file attachment |

This format is the internal Crush message representation. Clients should treat
it as opaque for import/export — the server handles serialization/deserialization.

#### `POST /sessions/import`

Import a session snapshot into the agent. If a session with the same ID
already exists, it is replaced (messages are deleted and re-created).

**Request:** A `SessionSnapshot` JSON object (same format as the export response).

**Response:**

```json
{
  "session_id": "ses_abc123",
  "message_count": 12,
  "status": "imported"
}
```

---

### SSE Event Stream

When `mode: "stream"` is used, the server returns an SSE stream. Each event
is a `data:` line containing a JSON `Event` object.

#### Event Types

| Type | Payload | Description |
|------|---------|-------------|
| `run.created` | `run` | Run has been created |
| `run.in-progress` | `run` | Agent is working |
| `run.completed` | `run` | Run finished successfully |
| `run.failed` | `run` | Run failed (check `run.error`) |
| `run.cancelled` | `run` | Run was cancelled |
| `run.awaiting` | `run` | Agent needs more input (check `run.await_request`) |
| `message.part` | `part` | Incremental text content delta |
| `message.completed` | `message` | Final assistant message |
| `session.message` | `generic` | Raw session message update (for crash recovery) |
| `session.snapshot` | `generic` | Full session export (emitted on completion) |
| `error` | `error` | Protocol-level error |

#### Event Format

```
data: {"type":"run.created","run":{"run_id":"...","status":"created",...}}

data: {"type":"run.in-progress","run":{"run_id":"...","status":"in-progress",...}}

data: {"type":"session.message","generic":{"event_type":"created","message_id":"msg_001","session_id":"ses_abc","role":"user","content":"Fix the bug"}}

data: {"type":"message.part","part":{"content_type":"text/plain","content":"I'll look at "}}

data: {"type":"message.part","part":{"content_type":"text/plain","content":"the auth.go file..."}}

data: {"type":"session.message","generic":{"event_type":"updated","message_id":"msg_002","session_id":"ses_abc","role":"assistant","content":"I'll look at the auth.go file...","tool_calls":[{"id":"tc_1","name":"view","input":"{\"file_path\":\"auth.go\"}","finished":true}]}}

data: {"type":"message.completed","message":{"role":"agent","parts":[{"content_type":"text/plain","content":"Done. I fixed the login bug."}]}}

data: {"type":"session.snapshot","generic":{"version":1,"session":{...},"messages":[...]}}

data: {"type":"run.completed","run":{"run_id":"...","status":"completed","output":[...]}}
```

#### `session.message` Event Detail

These events stream every internal message mutation (creates and updates) so
the client can track session state in real time. This is the key mechanism for
crash recovery — if the agent dies mid-run, the client has received all
messages up to the point of failure.

```json
{
  "event_type": "created|updated|deleted",
  "message_id": "msg_002",
  "session_id": "ses_abc123",
  "role": "assistant",
  "content": "I'll look at the auth.go file...",
  "tool_calls": [
    {"id": "tc_1", "name": "view", "input": "{\"file_path\":\"auth.go\"}", "finished": true}
  ],
  "tool_results": [
    {"tool_call_id": "tc_1", "name": "view", "content": "package auth...", "is_error": false}
  ]
}
```

#### `session.snapshot` Event Detail

Emitted once when a run completes successfully. Contains the full exportable
session in the same format as `GET /sessions/{id}/export`. Clients should
persist this snapshot — it can be imported into a new agent to resume.

---

## Client Patterns

### Pattern 1: Simple One-Shot

No session management. Fire and forget.

```python
response = requests.post(f"{BASE}/runs", json={
    "agent_name": "crush",
    "input": [{"role": "user", "parts": [{"content_type": "text/plain", "content": "Explain auth.go"}]}],
    "mode": "sync"
})
result = response.json()
print(result["output"][0]["parts"][0]["content"])
```

### Pattern 2: Multi-Turn Session

Reuse a session ID across multiple prompts.

```python
SESSION_ID = "project-42-auth-fix"

def send_prompt(text):
    response = requests.post(f"{BASE}/runs", json={
        "agent_name": "crush",
        "input": [{"role": "user", "parts": [{"content_type": "text/plain", "content": text}]}],
        "session_id": SESSION_ID,
        "mode": "sync"
    })
    return response.json()

send_prompt("Look at the auth module and identify bugs")
send_prompt("Fix the most critical one")
send_prompt("Now write tests for it")
```

### Pattern 3: Streaming with Crash Recovery

The recommended pattern for production orchestrators running agents in
ephemeral pods.

```python
import json
import sseclient  # pip install sseclient-py

SESSION_ID = "project-42-auth-fix"
last_snapshot = None  # Persist this to your storage backend

def run_with_recovery(agent_url, prompt):
    global last_snapshot

    # If we have a prior snapshot from a crashed agent, import it first.
    if last_snapshot:
        requests.post(f"{agent_url}/sessions/import", json=last_snapshot)
        last_snapshot = None

    # Stream the run.
    response = requests.post(f"{agent_url}/runs", json={
        "agent_name": "crush",
        "input": [{"role": "user", "parts": [{"content_type": "text/plain", "content": prompt}]}],
        "session_id": SESSION_ID,
        "mode": "stream"
    }, stream=True)

    client = sseclient.SSEClient(response)
    session_messages = []

    for event in client.events():
        data = json.loads(event.data)
        event_type = data["type"]

        if event_type == "message.part":
            # Stream text to UI.
            print(data["part"]["content"], end="", flush=True)

        elif event_type == "session.message":
            # Accumulate for crash recovery.
            session_messages.append(data["generic"])

        elif event_type == "session.snapshot":
            # Persist the final snapshot — this is the recovery checkpoint.
            last_snapshot = data["generic"]
            save_to_storage(SESSION_ID, last_snapshot)

        elif event_type == "run.completed":
            print("\n[done]")
            return data["run"]

        elif event_type == "run.failed":
            print(f"\n[failed: {data['run'].get('error', {}).get('message', 'unknown')}]")
            # On failure, export the session for recovery.
            export = requests.get(f"{agent_url}/sessions/{SESSION_ID}/export")
            if export.ok:
                last_snapshot = export.json()
                save_to_storage(SESSION_ID, last_snapshot)
            raise RuntimeError("agent run failed")

# Usage:
# If the agent crashes, spin up a new one and call run_with_recovery again.
# The snapshot is imported and the session continues.
```

### Pattern 4: Orchestrator with Pod Lifecycle

Full lifecycle for ephemeral Kubernetes pods.

```python
class AgentSession:
    def __init__(self, session_id, storage):
        self.session_id = session_id
        self.storage = storage  # e.g., S3, Redis, Postgres
        self.agent_url = None

    def ensure_agent(self):
        """Spin up an agent pod if needed."""
        if not self.agent_url or not self._is_healthy():
            self.agent_url = self._spawn_pod()
            snapshot = self.storage.get(self.session_id)
            if snapshot:
                requests.post(f"{self.agent_url}/sessions/import", json=snapshot)

    def run(self, prompt):
        """Run a prompt with automatic recovery."""
        self.ensure_agent()
        try:
            return self._stream_run(prompt)
        except (ConnectionError, Timeout):
            # Agent crashed mid-run. Export what we can from the streamed events.
            # On next call, ensure_agent() will import the snapshot.
            return None

    def teardown(self):
        """Clean shutdown — export session before killing the pod."""
        if self.agent_url:
            response = requests.get(
                f"{self.agent_url}/sessions/{self.session_id}/export"
            )
            if response.ok:
                self.storage.put(self.session_id, response.json())
            self._kill_pod()

    def _is_healthy(self):
        try:
            return requests.get(f"{self.agent_url}/ping", timeout=2).text == "pong"
        except Exception:
            return False
```

---

## Error Handling

All errors return JSON with the `ACPError` format:

```json
{
  "code": 404,
  "message": "session \"ses_xxx\" not found: sql: no rows in result set"
}
```

| HTTP Status | Meaning |
|-------------|---------|
| 400 | Bad request (missing fields, invalid JSON) |
| 404 | Agent, run, or session not found |
| 409 | Conflict (e.g., cancelling a completed run) |
| 500 | Internal server error |
| 503 | Service unavailable (prompt submitter or session store not ready) |

---

## Session Lifecycle

```
                    ┌──────────────────┐
                    │  Client Storage  │
                    │  (S3/Redis/PG)   │
                    └────────┬─────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
              ▼              ▼              ▼
         ┌─────────┐   ┌─────────┐   ┌─────────┐
         │ Agent 1  │   │ Agent 2  │   │ Agent 3  │
         │ (pod)    │   │ (pod)    │   │ (pod)    │
         └─────────┘   └─────────┘   └─────────┘
              │              │              │
         import ──→     import ──→     import ──→
         run + stream   run + stream   run + stream
         ←── snapshot   ←── snapshot   ←── snapshot
         teardown       crash!         teardown
```

1. **First run**: No snapshot exists. Agent creates a fresh session.
2. **Subsequent runs**: Client imports the last snapshot. Agent loads the full
   conversation and continues.
3. **Crash recovery**: Client has `session.message` events streamed up to the
   crash point. It can either:
   - Export from the crashed agent if still reachable: `GET /sessions/{id}/export`
   - Use the last `session.snapshot` event received before the crash
   - Accept the loss of messages since the last snapshot and continue
4. **Teardown**: Client exports the session before killing the pod.

---

## Health Check

```
GET /ping → "pong"
```

Use this for readiness probes. The server is ready when it returns `pong`.

---

## Limits and Considerations

- **Run TTL**: Completed runs are garbage collected after 1 hour. Export
  sessions before that if you need the data.
- **Concurrent runs**: Multiple runs can execute against different session IDs
  simultaneously. Runs against the *same* session ID are serialized by the
  Crush coordinator.
- **Snapshot size**: Snapshots include the full message `parts` JSON which can
  be large for long sessions with tool results. Crush's internal summarization
  reduces context window usage but the full history is preserved in the export.
- **Session import is destructive**: Importing replaces all existing messages
  for that session ID. It does not merge.
- **Message parts are opaque**: The `parts` JSON string in session messages
  uses Crush's internal format. Clients should treat it as opaque — round-trip
  it through export/import without modification.
