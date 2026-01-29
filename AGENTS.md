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
- `SnapshotText(snap)` - Extract text from terminal snapshot
- `WaitForText(t, term, text, timeout)` - Wait for text to appear
- `WaitForCondition(t, term, fn, timeout)` - Wait for condition

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
        "headers": {
          "Authorization": "Bearer token"
        }
      }
    }
  }
}
```

### What's Traced

- **Session spans** - Root spans for each chat session
- **User messages** - Spans with message content
- **Assistant messages** - Spans with response content
- **Tool calls** - Spans with tool name, input, and output

### Span Attributes

| Attribute | Description |
|-----------|-------------|
| `session.id` | Chat session identifier |
| `message.id` | Message identifier |
| `message.role` | user/assistant/tool |
| `message.content` | Text content (truncated) |
| `tool.id` | Tool call identifier |
| `tool.name` | Name of the tool |
| `tool.input` | JSON input (truncated) |
| `tool.result` | Result content (truncated) |
| `tool.is_error` | Whether tool returned error |

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
