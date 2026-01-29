# Crush Modules

A collection of plugins for [Crush](https://github.com/charmbracelet/crush), the AI coding assistant.

## Overview

This repository contains modular plugins that extend Crush with additional functionality. Plugins are compiled into a custom Crush binary using the `xcrush` build tool.

## Available Plugins

### OTLP Tracing (`otlp`)

Exports OpenTelemetry traces to an OTLP-compatible backend (Jaeger, Zipkin, OpenTelemetry Collector).

**Features:**
- Traces chat sessions, user messages, assistant responses, and tool calls
- Configurable endpoint, service name, and headers
- Supports both secure and insecure connections

**Configuration:**
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

### Agent Status (`agent-status`)

Reports agent status to a JSON file for external monitoring (e.g., status bars, dashboards).

**Features:**
- Writes status updates to `~/.agent-status/crush-{instance}.json`
- Tracks idle/thinking/working states
- Reports model, provider, token usage, and cost
- Configurable update interval

**Configuration:**
```json
{
  "options": {
    "plugins": {
      "agent-status": {
        "status_dir": "~/.agent-status",
        "update_interval_seconds": 1
      }
    }
  }
}
```

### Periodic Prompts (`periodic-prompts`)

Sends scheduled prompts to the LLM on a cron schedule.

**Features:**
- Configure prompts with crontab-style schedules
- Toggle periodic prompting via the `periodic_prompts` tool
- Prompts are queued if the agent is busy
- Supports tilde (`~`) expansion in file paths

**Configuration:**
```json
{
  "options": {
    "plugins": {
      "periodic-prompts": {
        "prompts": [
          {
            "file": "~/.config/crush/prompts/check-tests.md",
            "schedule": "*/30 * * * *",
            "name": "Run Tests"
          },
          {
            "file": "~/.config/crush/prompts/status.md",
            "schedule": "0 * * * *",
            "name": "Hourly Status"
          }
        ]
      }
    }
  }
}
```

**Usage:**
```
# In Crush chat, use the periodic_prompts tool:
periodic_prompts(action: "enable")   # Enable scheduled prompts
periodic_prompts(action: "disable")  # Disable scheduled prompts
periodic_prompts(action: "status")   # Check current state
periodic_prompts(action: "list")     # List configured prompts
```

### Ping (`ping`)

A simple test plugin for development and testing purposes.

## Installation

### Prerequisites

- Go 1.23 or later
- [Task](https://taskfile.dev/) (go-task)

### Building from Source

1. Clone this repository alongside the Crush source:
   ```bash
   git clone https://github.com/aleksclark/crush-modules.git
   git clone https://github.com/charmbracelet/crush.git crush-plugin-poc
   ```

2. Build the custom Crush binary with all plugins:
   ```bash
   cd crush-modules
   task distro
   ```

3. The binary will be at `./dist/crush`

### Build Options

```bash
# Build with all production plugins (default)
task distro

# Build with all plugins including test plugins
task distro:all

# Build with only specific plugins
task distro:otlp
task distro:agent-status
task distro:periodic-prompts
```

### Verify Installation

```bash
./dist/crush --list-plugins
```

Expected output:
```
Registered plugin tools:
  - periodic_prompts
Registered plugin hooks:
  - agent-status
  - periodic-prompts
  - otlp
```

## Configuration

Plugins are configured in your `crush.json` file (typically at `~/.config/crush/crush.json`).

### Disabling Plugins

To disable specific plugins:
```json
{
  "options": {
    "disabled_plugins": ["ping", "otlp"]
  }
}
```

### Plugin Configuration

Each plugin has its own configuration section under `options.plugins`:
```json
{
  "options": {
    "plugins": {
      "plugin-name": {
        "option1": "value1",
        "option2": "value2"
      }
    }
  }
}
```

## Development

### Running Tests

```bash
# Run unit tests (fast)
task test

# Run end-to-end tests (requires built binary)
task test:e2e
```

### Creating a New Plugin

See [AGENTS.md](./AGENTS.md) for detailed plugin development documentation.

1. Create a new directory for your plugin
2. Initialize a Go module with required dependencies
3. Implement either a Tool (invocable by the LLM) or a Hook (background processor)
4. Register your plugin in `init()`
5. Add to `Taskfile.yaml`
6. Write unit and e2e tests

### Project Structure

```
crush-modules/
├── agent-status/         # Agent status reporting hook
├── otlp/                 # OpenTelemetry tracing hook
├── periodic-prompts/     # Scheduled prompts hook + tool
├── ping/                 # Test plugin (excluded from production)
├── testutil/             # Shared test utilities
├── Taskfile.yaml         # Build and test commands
└── AGENTS.md             # Development documentation
```

## License

MIT
