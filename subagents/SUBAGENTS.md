# SubAgents Plugin Implementation

## Status: Partially Implemented

The core plugin structure is complete. Sub-agent execution requires a plugin API
extension in crush-plugin-poc.

## What's Implemented

### Files

```
subagents/
├── go.mod                 # Module dependencies
├── loader.go              # Agent file discovery and parsing
├── subagents.go           # Plugin entry, config, registry, tool
├── dialog_list.go         # SubAgents list dialog
├── dialog_details.go      # SubAgent details dialog
└── subagents_test.go      # Unit tests (all passing)
```

### Features Complete

1. **Agent File Parsing** (`loader.go`)
   - YAML frontmatter parsing with `gopkg.in/yaml.v3`
   - Markdown body extraction as system prompt
   - Tilde (~) and relative path expansion
   - Tool list parsing (comma-separated)
   - Validation of required fields (name, description)

2. **Registry** (`subagents.go`)
   - Singleton registry initialized on first tool load
   - Agent discovery from configured directories
   - Enable/disable agents at runtime
   - Reload individual agents or all from disk
   - First-match-wins for duplicate agent names

3. **SubAgent Tool** (`subagents.go`)
   - Tool registered as `subagent`
   - Dynamic tool description with available agents list
   - Validation of agent name and prompt
   - Placeholder response (execution not yet implemented)

4. **List Dialog** (`dialog_list.go`)
   - Shows all discovered agents with enabled status
   - Checkbox toggle with space
   - Enter to open details
   - 'r' to reload all agents
   - Sorted alphabetically by name

5. **Details Dialog** (`dialog_details.go`)
   - Shows agent metadata (file, model, tools, status)
   - View Prompt action (scrollable popup)
   - Toggle enable/disable
   - Reload from disk
   - Keyboard shortcuts (v, t, r)

6. **Configuration**
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

## What's NOT Implemented

### Sub-Agent Execution

The SubAgent tool currently returns a placeholder message:
```
Sub-agent execution not yet implemented. Would invoke agent 'code-reviewer' with prompt: ...
```

To enable execution, `crush-plugin-poc` needs:

1. **SubAgentRunner Interface**
   ```go
   // In plugin/app.go
   type SubAgentRunner interface {
       RunSubAgent(ctx context.Context, opts SubAgentOptions) (string, error)
   }
   
   type SubAgentOptions struct {
       SystemPrompt   string
       Prompt         string
       Tools          []string  // nil = inherit all
       DisallowedTools []string
       Model          string    // "inherit", "sonnet", "opus", "haiku"
       PermissionMode string
   }
   ```

2. **App Extension**
   ```go
   func (a *App) SubAgentRunner() SubAgentRunner
   ```

3. **Integration** in `coordinator.go` to wire up the runner

### Tool Permission Bypass

Agents should be able to specify allowed tools that don't prompt for permission.
This requires coordination with Crush's permission system.

## Agent File Format

```yaml
---
name: code-reviewer
description: Expert code reviewer for quality checks
tools: Read, Grep, Glob
disallowedTools: Bash, Write
model: inherit
permissionMode: default
---

You are a senior code reviewer...
```

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `name` | Yes | - | Unique identifier |
| `description` | Yes | - | When to use this agent |
| `tools` | No | inherit all | Comma-separated allowed tools |
| `disallowedTools` | No | none | Comma-separated denied tools |
| `model` | No | `inherit` | Model to use |
| `permissionMode` | No | `default` | Permission handling |

## Testing

```bash
# Run unit tests
cd subagents && go test -v

# Build distro with subagents
cd /path/to/crush-modules && go-task distro:subagents

# Verify registration
./dist/crush-subagents --list-plugins
```

## Next Steps

1. Add `SubAgentRunner` interface to `crush-plugin-poc/plugin/app.go`
2. Implement runner in `crush-plugin-poc/internal/agent/coordinator.go`
3. Wire up in tool factory with `app.SubAgentRunner()`
4. Add permission bypass for agent's allowed tools
5. Add e2e tests with mock LLM
