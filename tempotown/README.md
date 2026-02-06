# Tempotown Plugin

Integrates Crush with the [Tempotown](https://github.com/aleksclark/tempotown) orchestrator, enabling Crush to participate in a Temporal-based ensemble of AI coding agents.

## Overview

The tempotown plugin acts as an MCP client that connects to Tempotown's MCP server. It:

1. **Registers** Crush as an agent with the orchestrator
2. **Reports status** as Crush processes messages and executes tools
3. **Receives signals** from Temporal workflows via polling

## Configuration

Add to your `crush.json`:

```json
{
  "options": {
    "plugins": {
      "tempotown": {
        "endpoint": "localhost:9090",
        "role": "coder",
        "capabilities": ["code", "test", "review"],
        "poll_interval_seconds": 5
      }
    }
  }
}
```

### Options

| Option | Default | Description |
|--------|---------|-------------|
| `endpoint` | `localhost:9090` | Tempotown MCP server address |
| `role` | `coder` | Agent role: `coder`, `reviewer`, `merger`, `supervisor` |
| `capabilities` | `[]` | List of capabilities this agent provides |
| `poll_interval_seconds` | `5` | How often to poll for incoming signals |

## Expected Behavior

### Startup Sequence

1. Plugin starts a background connection loop
2. Connects to Tempotown MCP server via TCP
3. Performs MCP protocol initialization (`initialize` + `initialized`)
4. Calls `register_agent` with configured role and capabilities
5. Begins status reporting and signal polling

### Connection Management

- **Auto-reconnect**: If connection drops, waits 5 seconds then reconnects
- **Graceful degradation**: If Tempotown is unavailable, Crush continues normally
- **Non-blocking**: Connection issues don't block Crush's main functionality

### Status Reporting

The plugin observes Crush message events and reports status to Tempotown:

| Event | Status Reported | Progress |
|-------|-----------------|----------|
| User message created | `"processing user input"` | 0% |
| Assistant message created | `"generating response"` | 50% |
| Tool executing | `"running tool: {name}"` | 50% |
| Response complete | `"response complete"` | 100% |

Status updates are sent asynchronously and don't block Crush operations.

### Signal Reception

The plugin polls `get_pending_feedback` at the configured interval. Received feedback is placed on an internal channel that can be consumed by other components.

Supported signal types from Tempotown:
- `feedback` - Human feedback messages
- `nudge` - Prompts to continue or change direction
- `update_prompt` - System prompt modifications
- `shutdown` - Graceful shutdown request

### MCP Protocol

The plugin implements a subset of the MCP (Model Context Protocol) as a client:

**Methods used:**
- `initialize` - Protocol handshake
- `tools/call` - Invoke Tempotown tools

**Notifications sent:**
- `initialized` - Confirm initialization complete

**Tools called:**
- `register_agent` - Register with orchestrator on connect
- `report_status` - Send status updates during work
- `get_pending_feedback` - Poll for incoming signals

## Architecture

```
┌─────────────┐         TCP          ┌──────────────────┐
│   Crush     │◄────────────────────►│    Tempotown     │
│  (Client)   │    MCP Protocol      │    (Server)      │
└─────────────┘                      └──────────────────┘
      │                                      │
      │ Message Events                       │ Temporal
      ▼                                      ▼
┌─────────────┐                      ┌──────────────────┐
│  tempotown  │                      │    Workflows     │
│   plugin    │                      │   & Activities   │
└─────────────┘                      └──────────────────┘
```

## Logging

The plugin logs to Crush's standard logger with `hook=tempotown`:

```
INFO connected to Tempotown hook=tempotown agent_id=agent-12345
WARN failed to connect to Tempotown hook=tempotown error="dial failed: ..." endpoint=localhost:9090
INFO connection lost, reconnecting... hook=tempotown
```

## Failure Modes

### Tempotown Server Unavailable

- Plugin retries connection every 5 seconds
- Crush continues operating normally
- Status updates are silently dropped
- No user-visible errors

### Connection Dropped Mid-Session

- Plugin detects disconnect via read error
- Marks connection as disconnected
- Attempts reconnection after delay
- Re-registers agent on successful reconnect

### Slow Tempotown Response

- RPC calls have 30-second timeout
- Timed-out requests are cleaned up
- Status reporting is async (non-blocking)

## Integration with Tempotown

### Agent Roles

| Role | Description |
|------|-------------|
| `coder` | Implements features, writes code |
| `reviewer` | Reviews code changes |
| `merger` | Handles merge conflicts |
| `supervisor` | Coordinates other agents |

### Workflow Integration

When Tempotown orchestrates multiple agents:

1. Supervisor creates tasks via `create_task`
2. Coders claim tasks via `claim_task` (creates git worktree)
3. Agents report progress via `report_status`
4. Completed work submitted via `submit_result`
5. Reviewers receive review requests via signals

### Git Worktree Isolation

Tempotown creates isolated git worktrees for each task. When Crush claims a task, it should:

1. Work within the assigned worktree path
2. Commit changes to the task branch
3. Submit results when complete

## Development

### Running Tests

```bash
cd tempotown
go test -v ./...
```

### Mock Server

Tests use `mockMCPServer` which simulates Tempotown's MCP server:

```go
server := newMockMCPServer(t)
defer server.close()

// Server responds to register_agent, report_status, etc.
```

## Future Enhancements

- [ ] Task claiming via `claim_task` tool
- [ ] Work submission via `submit_result` tool
- [ ] Signal injection into Crush chat (requires plugin API extension)
- [ ] Worktree path awareness for file operations
- [ ] Direct Temporal client for richer signal handling
