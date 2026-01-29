package periodicprompts

import (
	"context"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/plugin"
)

// Tool implements the periodic_prompts tool for enabling/disabling via chat.
type Tool struct {
	app *plugin.App
}

// NewTool creates a new periodic prompts tool.
func NewTool(app *plugin.App) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ToolName,
		Description,
		func(ctx context.Context, params ToolParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			hook := getHook()
			if hook == nil {
				return fantasy.NewTextResponse("periodic-prompts hook is not initialized"), nil
			}

			switch strings.ToLower(params.Action) {
			case "status":
				return statusAction(hook), nil
			case "enable":
				return enableAction(hook), nil
			case "disable":
				return disableAction(hook), nil
			case "list":
				return listAction(hook), nil
			default:
				return fantasy.NewTextResponse(fmt.Sprintf("unknown action: %s (valid: status, enable, disable, list)", params.Action)), nil
			}
		},
	)
}

func statusAction(hook *Hook) fantasy.ToolResponse {
	status := "disabled"
	if hook.IsEnabled() {
		status = "enabled"
	}

	prompts := hook.GetPrompts()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Periodic prompting is %s.\n", status))
	sb.WriteString(fmt.Sprintf("Configured prompts: %d\n", len(prompts)))

	return fantasy.NewTextResponse(sb.String())
}

func enableAction(hook *Hook) fantasy.ToolResponse {
	hook.SetEnabled(true)

	prompts := hook.GetPrompts()
	if len(prompts) == 0 {
		return fantasy.NewTextResponse("Periodic prompting enabled, but no prompts are configured.")
	}

	return fantasy.NewTextResponse(fmt.Sprintf("Periodic prompting enabled. %d prompt(s) scheduled.", len(prompts)))
}

func disableAction(hook *Hook) fantasy.ToolResponse {
	hook.SetEnabled(false)
	return fantasy.NewTextResponse("Periodic prompting disabled.")
}

func listAction(hook *Hook) fantasy.ToolResponse {
	prompts := hook.GetPrompts()
	if len(prompts) == 0 {
		return fantasy.NewTextResponse("No periodic prompts configured.")
	}

	var sb strings.Builder
	sb.WriteString("Configured periodic prompts:\n\n")

	for i, p := range prompts {
		name := p.Name
		if name == "" {
			name = p.File
		}
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, name))
		sb.WriteString(fmt.Sprintf("   File: %s\n", p.File))
		sb.WriteString(fmt.Sprintf("   Schedule: %s\n", p.Schedule))
		sb.WriteString("\n")
	}

	status := "disabled"
	if hook.IsEnabled() {
		status = "enabled"
	}
	sb.WriteString(fmt.Sprintf("Status: %s", status))

	return fantasy.NewTextResponse(sb.String())
}
