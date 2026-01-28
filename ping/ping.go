// Package ping provides a simple "ping" tool for testing the Crush plugin system.
//
// When the agent calls ping(), the tool responds with "pong" (or a configured response).
// This serves as a proof-of-concept for the plugin architecture.
package ping

import (
	"context"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/plugin"
)

const (
	// ToolName is the name of the ping tool.
	ToolName = "ping"

	// Description is the tool description shown to the LLM.
	Description = `A simple test tool that responds with "pong" when called.

<usage>
Call this tool to verify the plugin system is working correctly.
No parameters are required.
</usage>

<example>
ping() -> "pong"
</example>
`

	// DefaultResponse is the default response when no config is provided.
	DefaultResponse = "pong"
)

// Config defines the configuration options for the ping plugin.
type Config struct {
	// ResponseString is the string to respond with. Defaults to "pong".
	ResponseString string `json:"response_string,omitempty"`
}

// PingParams defines the parameters for the ping tool (none required).
type PingParams struct{}

func init() {
	plugin.RegisterToolWithConfig(ToolName, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
		var cfg Config
		if err := app.LoadConfig(ToolName, &cfg); err != nil {
			return nil, err
		}
		response := cfg.ResponseString
		if response == "" {
			response = DefaultResponse
		}
		return NewPingToolWithResponse(response), nil
	}, &Config{})
}

// NewPingTool creates a new ping tool instance with default response.
func NewPingTool() fantasy.AgentTool {
	return NewPingToolWithResponse(DefaultResponse)
}

// NewPingToolWithResponse creates a ping tool with a custom response string.
func NewPingToolWithResponse(response string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ToolName,
		Description,
		func(ctx context.Context, params PingParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.NewTextResponse(response), nil
		},
	)
}
