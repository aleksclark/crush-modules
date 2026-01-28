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

// MyToolParams defines the parameters the LLM can pass.
type MyToolParams struct {
    Param string `json:"param" jsonschema:"description=What this param does"`
}

func init() {
    plugin.RegisterTool(ToolName, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
        // Access app.WorkingDir(), app.Logger(), app.ExtensionConfig() here
        return NewMyTool(), nil
    })
}

func NewMyTool() fantasy.AgentTool {
    return fantasy.NewAgentTool(
        ToolName,
        Description,
        func(ctx context.Context, params MyToolParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
            // Tool implementation
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

If you need a new hook point, follow this pattern:

1. Define the hook interface in `plugin/plugin.go`
2. Add registration function (e.g., `RegisterOnToolCall()`)
3. Call the hook from the appropriate place in crush-plugin-poc
4. Document the hook in this file

Example hook addition:

```go
// In plugin/plugin.go
type OnToolCallHook func(ctx context.Context, toolName string, input string) error

var onToolCallHooks []OnToolCallHook

func RegisterOnToolCall(hook OnToolCallHook) {
    onToolCallHooks = append(onToolCallHooks, hook)
}

func InvokeOnToolCall(ctx context.Context, toolName, input string) error {
    for _, hook := range onToolCallHooks {
        if err := hook(ctx, toolName, input); err != nil {
            return err
        }
    }
    return nil
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
