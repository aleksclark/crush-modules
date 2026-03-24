# Crush ACP SDK for Go

A high-level Go client for the Crush Agent Communication Protocol (ACP).
Provides session-oriented methods for multi-turn conversations with Crush
agents, including full session persistence via export/import.

## Install

```bash
go get github.com/aleksclark/crush-modules/sdk
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/aleksclark/crush-modules/sdk"
)

func main() {
    client := sdk.NewClient("http://localhost:8199")

    ctx := context.Background()

    // Start a new session.
    result, err := client.NewSession(ctx, "Fix the login bug in auth.go")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result.Text())
    fmt.Println("Session:", result.Run.SessionID)

    // Continue the conversation.
    result, err = client.Resume(ctx, result.Run.SessionID, "Now add tests for it")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result.Text())
}
```

## API

### Session Lifecycle

| Method | Description |
|--------|-------------|
| `NewSession(ctx, prompt)` | Start a new session, returns result with session ID |
| `Resume(ctx, sessionID, prompt)` | Continue an existing session |
| `Dump(ctx, sessionID)` | Export full session snapshot for persistence |
| `Restore(ctx, snapshot)` | Import a snapshot into the agent |

### Streaming

| Method | Description |
|--------|-------------|
| `NewSessionStream(ctx, prompt)` | Start session with streaming events |
| `ResumeStream(ctx, sessionID, prompt)` | Resume session with streaming events |

### Utilities

| Method | Description |
|--------|-------------|
| `Ping(ctx)` | Health check |
| `ListAgents(ctx)` | Discover available agents |
| `WaitReady(ctx, interval)` | Poll until server is ready |

## Streaming

```go
stream, err := client.NewSessionStream(ctx, "Explain auth.go")
if err != nil {
    log.Fatal(err)
}

for event := range stream.Events {
    switch event.Type {
    case sdk.EventMessagePart:
        fmt.Print(event.Part.Content) // real-time text deltas
    case sdk.EventRunCompleted:
        fmt.Println("\nDone")
    case sdk.EventRunFailed:
        fmt.Println("\nFailed:", event.Run.Error.Message)
    }
}
if err := stream.Err(); err != nil {
    log.Fatal(err)
}
```

Or use `stream.Result()` to block until completion:

```go
stream, _ := client.NewSessionStream(ctx, "Fix the bug")
result, err := stream.Result()
// result.Run, result.Snapshot are populated
```

## Session Persistence (Dump / Restore)

Save a session for crash recovery or migration between agent instances:

```go
// Dump the session.
snapshot, err := client.Dump(ctx, sessionID)
data, _ := json.Marshal(snapshot)
os.WriteFile("session.json", data, 0o644)

// Later (possibly on a different agent instance)...
data, _ = os.ReadFile("session.json")
var snapshot sdk.SessionSnapshot
json.Unmarshal(data, &snapshot)

err = client.Restore(ctx, &snapshot)
// Continue where we left off.
result, err := client.Resume(ctx, snapshot.Session.ID, "What were we working on?")
```

### Streaming Crash Recovery

Streaming runs emit `session.snapshot` events on completion. The SDK captures
these automatically:

```go
stream, _ := client.NewSessionStream(ctx, "Fix the bug")
result, _ := stream.Result()

if result.Snapshot != nil {
    // Persist for crash recovery.
    data, _ := json.Marshal(result.Snapshot)
    os.WriteFile("snapshot.json", data, 0o644)
}
```

## Options

```go
// Custom HTTP client.
client := sdk.NewClient(url, sdk.WithHTTPClient(&http.Client{
    Timeout: 30 * time.Second,
}))

// Custom headers (auth, tracing, etc).
client := sdk.NewClient(url, sdk.WithHeaders(map[string]string{
    "Authorization": "Bearer token",
}))

// Explicit agent name (skips auto-detection).
client := sdk.NewClient(url, sdk.WithAgentName("crush"))
```

## Waiting for Server Readiness

When spawning ephemeral agent containers:

```go
client := sdk.NewClient("http://agent-pod:8199")

ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := client.WaitReady(ctx, 500*time.Millisecond); err != nil {
    log.Fatal("agent not ready:", err)
}

// Now safe to use.
result, _ := client.NewSession(ctx, "Start working")
```

## Full Orchestrator Pattern

```go
func runAgentWorkflow(agentURL string, tasks []string, savedSnapshot *sdk.SessionSnapshot) error {
    client := sdk.NewClient(agentURL)

    ctx := context.Background()
    if err := client.WaitReady(ctx, time.Second); err != nil {
        return err
    }

    // Restore prior state if resuming after crash.
    var sessionID string
    if savedSnapshot != nil {
        if err := client.Restore(ctx, savedSnapshot); err != nil {
            return err
        }
        sessionID = savedSnapshot.Session.ID
    }

    for _, task := range tasks {
        var result *sdk.SessionResult
        var err error

        if sessionID == "" {
            result, err = client.NewSession(ctx, task)
        } else {
            result, err = client.Resume(ctx, sessionID, task)
        }
        if err != nil {
            return err
        }

        sessionID = result.Run.SessionID
        fmt.Println(result.Text())
    }

    // Save before teardown.
    snapshot, err := client.Dump(ctx, sessionID)
    if err != nil {
        return err
    }
    data, _ := json.Marshal(snapshot)
    return os.WriteFile("session-checkpoint.json", data, 0o644)
}
```
