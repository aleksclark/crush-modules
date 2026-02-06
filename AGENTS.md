# Crush Modules Development Guide

This repository contains plugins (modules) for the Crush AI assistant. Plugins
extend Crush with new tools that agents can invoke.

## Repository Structure

```
crush-modules/
├── ping/                  # Example plugin module
│   ├── go.mod             # Module-specific dependencies
│   ├── ping.go            # Tool implementation
│   ├── ping_test.go       # Unit tests
│   └── ping_e2e_test.go   # End-to-end tests
├── otlp/                  # OTLP tracing hook plugin
│   ├── go.mod             # Module-specific dependencies
│   ├── otlp.go            # Hook implementation
│   └── otlp_test.go       # Unit tests
├── testutil/              # Shared test utilities
│   └── testutil.go        # Terminal testing helpers
├── Taskfile.yaml          # Build and test commands
└── go.mod                 # Root module (test dependencies)
```

## Build/Test Commands

All commands use `go-task` (or `task`):

- **Build distro**: `task distro` - Build Crush with all plugins
- **Build single plugin**: `task distro:ping` - Build with only ping plugin
- **Run all tests**: `task test` - Unit tests for all plugins
- **Run e2e tests**: `task test:e2e` - End-to-end tests (builds distro first)
- **List plugins**: `task list` - Show registered plugins in built binary
- **Clean**: `task clean` - Remove build artifacts

## Creating a New Plugin

### 1. Create the Module Directory

```bash
mkdir -p myplugin
cd myplugin
go mod init github.com/aleksclark/crush-modules/myplugin
```

### 2. Add Required Dependencies

Your `go.mod` needs:

```go
module github.com/aleksclark/crush-modules/myplugin

go 1.25.5

require (
    charm.land/fantasy v0.6.1
    github.com/aleksclark/crush-modules v0.0.0
    github.com/charmbracelet/crush v0.0.0
    github.com/stretchr/testify v1.11.1
)

replace github.com/charmbracelet/crush => ../../crush-plugin-poc
replace github.com/aleksclark/crush-modules => ../
```

Run `go mod tidy` to resolve transitive dependencies.

### 3. Implement the Tool

```go
package myplugin

import (
    "context"

    "charm.land/fantasy"
    "github.com/charmbracelet/crush/plugin"
)

const (
    ToolName    = "mytool"
    Description = `Description shown to the LLM.

<usage>
Explain when and how to use this tool.
</usage>

<example>
mytool(param: "value") -> "result"
</example>
`
)

// Config defines configuration options for this plugin.
// Users configure this in crush.json under options.plugins.mytool
type Config struct {
    APIKey  string `json:"api_key,omitempty"`
    Timeout int    `json:"timeout,omitempty"`
}

// MyToolParams defines the parameters the LLM can pass.
type MyToolParams struct {
    Param string `json:"param" jsonschema:"description=What this param does"`
}

func init() {
    // Register with config schema for validation
    plugin.RegisterToolWithConfig(ToolName, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
        // Load typed configuration
        var cfg Config
        if err := app.LoadConfig(ToolName, &cfg); err != nil {
            return nil, err
        }
        
        // Access other app services
        // app.WorkingDir(), app.Logger(), app.Permissions()
        return NewMyTool(cfg), nil
    }, &Config{})
}

func NewMyTool(cfg Config) fantasy.AgentTool {
    return fantasy.NewAgentTool(
        ToolName,
        Description,
        func(ctx context.Context, params MyToolParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
            // Tool implementation using cfg
            return fantasy.NewTextResponse("result"), nil
        },
    )
}
```

### 4. Add to Taskfile

Update `Taskfile.yaml` to include your plugin in the distro build:

```yaml
distro:
  cmds:
    - |
      {{.OUTPUT_DIR}}/xcrush build \
        --crush {{.CRUSH_POC}} \
        --with ./ping \
        --with ./myplugin \
        --output {{.OUTPUT_DIR}}/crush
```

## Plugin Configuration

Plugins can be configured and disabled via the user's `crush.json` file.

### Configuration Structure

```json
{
  "options": {
    "disabled_plugins": ["plugin_to_disable"],
    "plugins": {
      "ping": {
        "response_string": "custom response"
      },
      "myplugin": {
        "api_key": "sk-xxx",
        "timeout": 30
      }
    }
  }
}
```

### Disabling Plugins

Add plugin names to `options.disabled_plugins` to prevent them from loading:

```json
{
  "options": {
    "disabled_plugins": ["ping", "debug_tool"]
  }
}
```

### Plugin-Specific Config

Each plugin defines its own config schema. Use `app.LoadConfig()` to load
typed configuration:

```go
type Config struct {
    APIKey  string `json:"api_key"`
    Timeout int    `json:"timeout"`
}

func init() {
    plugin.RegisterToolWithConfig("myplugin", func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
        var cfg Config
        if err := app.LoadConfig("myplugin", &cfg); err != nil {
            return nil, err
        }
        // Use cfg.APIKey, cfg.Timeout
        return NewMyTool(cfg), nil
    }, &Config{})
}
```

Benefits of `RegisterToolWithConfig`:
- Config schema is stored for documentation/validation
- `app.LoadConfig()` handles JSON marshaling/unmarshaling
- Type mismatches are caught early with clear errors
- Missing config leaves struct with zero/default values

## Testing Requirements

**Every plugin MUST have comprehensive tests.** Testing is critical because
plugins run inside the agent loop and failures affect user experience.

### Unit Tests (Required)

Test the tool handler directly without the full Crush runtime:

```go
package myplugin

import (
    "context"
    "testing"

    "charm.land/fantasy"
    "github.com/stretchr/testify/require"
)

func TestMyToolBasicOperation(t *testing.T) {
    t.Parallel()

    tool := NewMyTool()

    // Verify tool metadata
    require.Equal(t, ToolName, tool.Info().Name)

    // Invoke the tool
    call := fantasy.ToolCall{
        ID:    "test-call",
        Name:  ToolName,
        Input: `{"param": "test-value"}`,
    }

    resp, err := tool.Run(context.Background(), call)
    require.NoError(t, err)
    require.Equal(t, "expected-result", resp.Content)
}

func TestMyToolErrorHandling(t *testing.T) {
    t.Parallel()

    tool := NewMyTool()

    // Test with invalid input
    call := fantasy.ToolCall{
        ID:    "test-call",
        Name:  ToolName,
        Input: `{"param": ""}`,
    }

    resp, err := tool.Run(context.Background(), call)
    require.NoError(t, err)  // Tools should return errors in response, not as Go errors
    require.True(t, resp.IsError)
}
```

### End-to-End Tests (Required)

E2E tests verify the plugin works in the built Crush binary:

```go
package myplugin_test

import (
    "testing"
    "time"

    "github.com/aleksclark/crush-modules/testutil"
    "github.com/stretchr/testify/require"
)

func TestMyPluginRegistered(t *testing.T) {
    testutil.SkipIfE2EDisabled(t)

    term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
    defer term.Close()

    time.Sleep(500 * time.Millisecond)

    snap := term.Snapshot()
    output := testutil.SnapshotText(snap)

    require.Contains(t, output, "mytool", "Expected mytool plugin to be registered")
}
```

### Test Utilities

The `testutil` package provides helpers for e2e testing:

- `SkipIfE2EDisabled(t)` - Skip if E2E_SKIP env var is set
- `CrushBinary()` - Path to the built Crush binary
- `NewTestTerminal(t, args, cols, rows)` - Create terminal with Crush running
- `NewIsolatedTerminal(t, cols, rows)` - Terminal with isolated config
- `NewIsolatedTerminalWithConfigAndEnv(t, cols, rows, configJSON, tmpDir)` - Full control
- `SnapshotText(snap)` - Extract text from terminal snapshot
- `WaitForText(t, term, text, timeout)` - Wait for text to appear
- `WaitForCondition(t, term, fn, timeout)` - Wait for condition

### Mock LLM Server

The `testutil/mockllm` package provides a mock OpenAI-compatible server for
testing agent behavior without making real API calls.

```go
package myplugin_test

import (
    "testing"

    "github.com/aleksclark/crush-modules/testutil"
    "github.com/aleksclark/crush-modules/testutil/mockllm"
    "github.com/stretchr/testify/require"
)

func TestPluginWithMockLLM(t *testing.T) {
    testutil.SkipIfE2EDisabled(t)

    // Create and configure mock server.
    server := mockllm.NewServer()
    server.OnMessage("ping", mockllm.ToolCallResponse("ping", map[string]any{}))
    server.OnToolResult("ping", mockllm.TextResponse("Pong received!"))
    url := server.Start(t)

    // Create isolated environment pointing to mock server.
    tmpDir := mockllm.SetupTestEnv(t, url)
    term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 80, 24, mockllm.TestConfig(url), tmpDir)
    defer term.Close()

    // Interact with the agent...
    testutil.WaitForText(t, term, "Pong", 10*time.Second)
}
```

#### Response Builders

| Function | Description |
|----------|-------------|
| `TextResponse(content)` | Simple text response |
| `ToolCallResponse(name, args)` | Single tool call |
| `MultiToolCallResponse(specs...)` | Multiple tool calls |
| `TextAndToolResponse(content, name, args)` | Text with tool call |
| `ErrorResponse(message)` | Error message |
| `EchoResponse(prefix)` | Echoes user message |
| `EmptyResponse()` | No content (edge case) |

#### Matchers

| Function | Description |
|----------|-------------|
| `MessageContains(text)` | Last user message contains text |
| `MessageEquals(text)` | Last user message equals text exactly |
| `HasToolResult(name)` | Has tool result with given name |
| `HasToolCall(name)` | Any assistant message has tool call |
| `HasSystemPrompt()` | Has a system message |
| `SystemPromptContains(text)` | System prompt contains text |
| `MessageCount(n)` | Exactly n messages |
| `And(matchers...)` | All matchers must match |
| `Or(matchers...)` | Any matcher must match |
| `Not(matcher)` | Negates a matcher |

#### Server Methods

| Method | Description |
|--------|-------------|
| `Start(t)` | Start server, returns URL |
| `OnMessage(text, resp)` | Handle messages containing text |
| `OnToolResult(name, resp)` | Handle tool results |
| `OnAny(resp)` | Handle any request |
| `On(matcher, resp)` | Custom matcher |
| `Sequence(responses...)` | Return responses in order |
| `Default(resp)` | Default when no match |
| `Requests()` | Get all captured requests |
| `LastRequest()` | Get most recent request |
| `Reset()` | Clear handlers and history |

#### Conversation Builder

For complex multi-turn conversations:

```go
mockllm.NewConversation(server).
    ThenText("Hello! How can I help?").
    ThenTool("search", map[string]string{"query": "test"}).
    ThenText("Here are the search results.").
    Apply()
```

#### Assertions

```go
mockllm.AssertRequestCount(t, server, 3)
mockllm.AssertLastMessageContains(t, server, "hello")
mockllm.AssertToolWasCalled(t, server, "ping")
mockllm.AssertToolWasNotCalled(t, server, "dangerous_tool")
```

## Working with crush-plugin-poc

The plugin system is defined in `crush-plugin-poc`. When developing plugins,
you may need to extend the core plugin infrastructure.

### When to Modify crush-plugin-poc

You'll need to update crush-plugin-poc if your plugin requires:

1. **New hook points** - If you need to hook into agent lifecycle events
   (before/after tool execution, message handling, etc.)

2. **Extended App context** - If plugins need access to additional Crush
   internals (new fields in `plugin.App`)

3. **New tool capabilities** - If the `fantasy.AgentTool` interface doesn't
   support your use case

4. **Configuration schema changes** - If plugins need new configuration options

### Plugin System Files in crush-plugin-poc

```
crush-plugin-poc/
├── plugin/
│   ├── plugin.go      # Tool registration: RegisterTool(), GetToolFactory()
│   ├── app.go         # App context passed to tool factories
│   └── plugin_test.go # Plugin system tests
├── cmd/xcrush/        # Build tool for creating plugin-enabled binaries
│   ├── main.go
│   ├── build.go       # xcrush build command
│   └── list.go        # xcrush list command
├── cmd/crush/
│   └── crush.go       # Public entry point for external builds
└── internal/
    ├── agent/coordinator.go  # Plugin tool loading (lines 413-430)
    ├── config/config.go      # Extensions config field (line 387)
    └── cmd/root.go           # --list-plugins flag
```

### Adding New Hooks

The plugin system supports two types of extensions:
- **Tools** - Agent tools that can be invoked during chat (`RegisterTool`)
- **Hooks** - Background processors that observe system events (`RegisterHook`)

#### Creating a Hook Plugin

Hooks run in the background and can subscribe to message events:

```go
package myhook

import (
    "context"
    "github.com/charmbracelet/crush/plugin"
)

const HookName = "myhook"

type Config struct {
    Endpoint string `json:"endpoint,omitempty"`
}

func init() {
    plugin.RegisterHookWithConfig(HookName, func(ctx context.Context, app *plugin.App) (plugin.Hook, error) {
        var cfg Config
        if err := app.LoadConfig(HookName, &cfg); err != nil {
            return nil, err
        }
        return NewMyHook(app, cfg)
    }, &Config{})
}

type MyHook struct {
    app *plugin.App
    cfg Config
}

func NewMyHook(app *plugin.App, cfg Config) (*MyHook, error) {
    return &MyHook{app: app, cfg: cfg}, nil
}

func (h *MyHook) Name() string {
    return HookName
}

func (h *MyHook) Start(ctx context.Context) error {
    messages := h.app.Messages()
    if messages == nil {
        return nil // No message subscriber available
    }

    events := messages.SubscribeMessages(ctx)
    for {
        select {
        case <-ctx.Done():
            return h.Stop()
        case event, ok := <-events:
            if !ok {
                return nil
            }
            h.handleEvent(event)
        }
    }
}

func (h *MyHook) Stop() error {
    return nil
}

func (h *MyHook) handleEvent(event plugin.MessageEvent) {
    switch event.Type {
    case plugin.MessageCreated:
        // Handle new message
    case plugin.MessageUpdated:
        // Handle message update (e.g., streaming, tool calls)
    case plugin.MessageDeleted:
        // Handle message deletion
    }
}
```

#### Message Event Types

Hooks can observe:
- `plugin.MessageCreated` - New message created
- `plugin.MessageUpdated` - Message content updated (streaming, tool calls)
- `plugin.MessageDeleted` - Message removed

#### Message Structure

```go
type Message struct {
    ID        string
    SessionID string
    Role      MessageRole  // user, assistant, system, tool
    Content   string
    ToolCalls   []ToolCallInfo
    ToolResults []ToolResultInfo
}

type ToolCallInfo struct {
    ID       string
    Name     string
    Input    string  // JSON input
    Finished bool
}

type ToolResultInfo struct {
    ToolCallID string
    Name       string
    Content    string
    IsError    bool
}
```

## OTLP Tracing Plugin

The `otlp` plugin exports traces to an OTLP-compatible backend (Jaeger, Zipkin,
OpenTelemetry Collector).

### Configuration

```json
{
  "options": {
    "plugins": {
      "otlp": {
        "endpoint": "http://localhost:4318",
        "service_name": "crush",
        "insecure": true,
        "content_limit": 4000,
        "tool_input_limit": 4000,
        "tool_result_limit": 4000,
        "headers": {
          "Authorization": "Bearer token"
        }
      }
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `endpoint` | `http://localhost:4318` | OTLP HTTP endpoint |
| `service_name` | `crush` | Service name in traces |
| `insecure` | `false` | Allow HTTP (not HTTPS) |
| `content_limit` | `4000` | Max chars for message content |
| `tool_input_limit` | `4000` | Max chars for tool input |
| `tool_result_limit` | `4000` | Max chars for tool results |
| `headers` | `{}` | Headers for OTLP requests |

### What's Traced

- **Session spans** - Root spans with project/git context
- **User messages** - Spans with full message content
- **Assistant messages** - Spans with response content and LLM metrics
- **Tool calls** - Spans with tool name, input, result, and semantic attributes

### Session Span Attributes

| Attribute | Description |
|-----------|-------------|
| `session.id` | Chat session identifier |
| `agent.name` | Agent name ("crush") |
| `project.path` | Working directory path |
| `project.name` | Project folder name |
| `git.repo` | Git remote origin (normalized) |
| `git.branch` | Current git branch |
| `llm.model` | AI model identifier |
| `llm.provider` | API provider (anthropic, bedrock, etc.) |

### Message Span Attributes

| Attribute | Description |
|-----------|-------------|
| `message.id` | Message identifier |
| `message.role` | user/assistant/tool |
| `message.content` | Full text content |
| `message.content_length` | Original content length |
| `message.tool_calls` | Number of tool calls (assistant only) |
| `llm.model` | Model used for response |
| `llm.provider` | API provider |
| `llm.tokens.input` | Input tokens consumed |
| `llm.tokens.output` | Output tokens generated |
| `llm.tokens.cache_read` | Tokens read from cache |
| `llm.tokens.cache_write` | Tokens written to cache |
| `llm.cost_usd` | Estimated cost in USD |

### Tool Span Attributes

| Attribute | Description |
|-----------|-------------|
| `tool.id` | Tool call identifier |
| `tool.name` | Name of the tool |
| `tool.input` | Full JSON input |
| `tool.result` | Result content |
| `tool.result_length` | Original result length |
| `tool.is_error` | Whether tool returned error |
| `tool.target_file` | Target file path (if applicable) |
| `tool.target_url` | Target URL (for fetch ops) |
| `tool.search_pattern` | Search pattern (for grep/glob) |
| `tool.command` | Command string (for bash) |
| `tool.param.*` | Individual tool parameters |

## Agent Status Plugin

The `agent-status` plugin reports the agent's current state to a JSON file that
external tools can monitor. This implements the protocol defined at:
https://github.com/aleksclark/go-turing-smart-screen/blob/master/AGENT_STATUS_REPORTING.md

### Configuration

```json
{
  "options": {
    "plugins": {
      "agent-status": {
        "status_dir": "~/.agent-status",
        "update_interval_seconds": 10
      }
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `status_dir` | `~/.agent-status` | Directory for status files. Supports `~` expansion. |
| `update_interval_seconds` | `10` | How often to update the file (minimum). |

### Status File Format

The plugin writes JSON files named `crush-{instance}.json` with:

| Field | Type | Description |
|-------|------|-------------|
| `v` | int | Schema version (always 1) |
| `agent` | string | Agent type ("crush") |
| `instance` | string | Unique instance ID |
| `status` | string | Current status (idle, thinking, working, etc.) |
| `updated` | int | Unix timestamp of last update |
| `pid` | int | Process ID |
| `cwd` | string | Current working directory |
| `task` | string | Current task description |
| `tools.active` | string | Currently running tool |
| `tools.recent` | []string | Last 10 tools used |
| `tools.counts` | map | Tool invocation counts |

### Status Values

- `idle` - Waiting for user input
- `thinking` - Processing/reasoning
- `working` - Actively executing tools
- `error` - Encountered an error

## SubAgents Plugin

The `subagents` plugin enables custom sub-agents loaded from YAML+Markdown files.
Sub-agents are specialized agents with their own system prompts and tool access.

### Configuration

```json
{
  "options": {
    "plugins": {
      "subagents": {
        "dirs": [".crush/agents", "~/.crush/agents"]
      }
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `dirs` | `[".crush/agents", "~/.crush/agents"]` | Directories to search for agent files |

### Agent File Format

Agent files are Markdown with YAML frontmatter:

```yaml
---
name: code-reviewer
description: Expert code reviewer for quality checks
tools: Read, Grep, Glob
model: inherit
permissionMode: default
---

You are a senior code reviewer with expertise in Go and TypeScript.
Review code changes carefully for:
- Logic errors
- Security issues
- Performance problems
```

### Frontmatter Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Unique identifier (lowercase, hyphens) |
| `description` | Yes | When to delegate to this agent |
| `tools` | No | Allowed tools (comma-separated). Inherits all if omitted |
| `disallowedTools` | No | Tools to deny |
| `model` | No | `sonnet`, `opus`, `haiku`, `inherit` (default: `inherit`) |
| `permissionMode` | No | `default`, `acceptEdits`, `dontAsk`, `bypassPermissions`, `plan` |

### Dialogs

The plugin provides two dialogs accessible via ctrl+p:

1. **SubAgents List** - Shows all discovered sub-agents with enabled status
2. **SubAgent Details** - View prompt, toggle, reload individual agents

### Current Limitations

Sub-agent execution requires plugin API extension (not yet implemented).
Currently, the tool returns a placeholder message. Full execution requires:
- `plugin.App.SubAgentRunner()` interface for running sub-agents
- Tool permission bypass for agent's allowed tools

## Tempotown Plugin

The `tempotown` plugin integrates Crush with the Tempotown orchestrator, allowing
Crush to act as an agent within a Temporal-based ensemble of AI coding agents.

### What It Does

1. **Reports Status** - Sends Crush's current activity to Tempotown via MCP
2. **Receives Signals** - Polls for feedback/signals from Temporal workflows
3. **Auto-Reconnects** - Maintains persistent connection to Tempotown server

### Configuration

```json
{
  "options": {
    "plugins": {
      "tempotown": {
        "endpoint": "localhost:9090",
        "role": "coder",
        "capabilities": ["code", "test"],
        "poll_interval_seconds": 5
      }
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `endpoint` | `localhost:9090` | Tempotown MCP server address |
| `role` | `coder` | Agent role: coder, reviewer, merger, supervisor |
| `capabilities` | `[]` | List of agent capabilities |
| `poll_interval_seconds` | `5` | How often to poll for signals |

### How It Works

1. **Connection** - On startup, connects to Tempotown MCP server at configured endpoint
2. **Registration** - Calls `register_agent` with configured role and capabilities
3. **Status Reporting** - Observes Crush message events and reports status via `report_status`
4. **Signal Polling** - Periodically polls `get_pending_feedback` for incoming signals

### Status Events Reported

| Crush Event | Status Reported |
|-------------|-----------------|
| User message received | "processing user input" |
| Assistant generating | "generating response" |
| Tool executing | "running tool: {name}" |
| Response complete | "response complete" |

### Tempotown MCP Tools Used

| Tool | Purpose |
|------|---------|
| `register_agent` | Register Crush instance with orchestrator |
| `report_status` | Send status updates during work |
| `get_pending_feedback` | Receive signals from workflows |

### Requirements

- Tempotown orchestrator running at configured endpoint
- MCP server enabled (default port 9090)

### Feedback Channel

The plugin exposes a feedback channel for injecting signals into Crush. This can
be used by other components to receive workflow signals:

```go
hook := h.(*tempotown.TempotownHook)
for feedback := range hook.FeedbackCh() {
    // Handle feedback from Tempotown
    log.Println("Received:", feedback.Message)
}
```

## Code Style

Follow the same conventions as crush-plugin-poc (see its AGENTS.md):

- Use `gofumpt` for formatting
- Use `testify/require` for assertions
- Use `t.Parallel()` for independent tests
- Comments on their own line end with periods
- Use semantic commits (`feat:`, `fix:`, `chore:`, etc.)

## Debugging

### Check Plugin Registration

```bash
./dist/crush --list-plugins
```

### Build with Verbose Output

```bash
./dist/xcrush build --crush ../crush-plugin-poc --with ./myplugin --output ./dist/crush --skip-clean
```

The `--skip-clean` flag preserves the temp build directory for inspection.

### Run Specific Tests

```bash
cd myplugin
go test -v -run TestMyTool
```

### Skip E2E Tests During Development

```bash
E2E_SKIP=1 go test ./...
```
