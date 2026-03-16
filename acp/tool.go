package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/plugin"
)

const (
	// ToolListAgents is the tool name for listing remote ACP agents.
	ToolListAgents = "acp_list_agents"

	// ToolRunAgent is the tool name for invoking a remote ACP agent.
	ToolRunAgent = "acp_run_agent"

	// ToolResumeRun is the tool name for resuming an awaiting ACP run.
	ToolResumeRun = "acp_resume_run"

	// DescriptionListAgents is shown to the LLM for acp_list_agents.
	DescriptionListAgents = `List available remote agents on ACP servers.

<usage>
Use this tool to discover what remote AI agents are available before invoking them.
Returns agent names, descriptions, and capabilities.
</usage>

<example>
acp_list_agents() -> [{"name": "summarizer", "description": "Summarizes documents"}]
acp_list_agents(server: "research") -> agents from specific server
</example>
`

	// DescriptionRunAgent is shown to the LLM for acp_run_agent.
	DescriptionRunAgent = `Invoke a remote ACP agent with a message and get its response.

<usage>
Send a text message to a remote agent and receive its response. The agent runs on a
remote ACP server. Use acp_list_agents first to discover available agents.

If the agent enters an "awaiting" state (needs more input), the response will indicate
this with the run_id. Use acp_resume_run to provide the requested input.
</usage>

<example>
acp_run_agent(agent_name: "summarizer", input: "Summarize this document: ...") -> "Here is the summary..."
acp_run_agent(agent_name: "researcher", input: "Find papers on quantum computing", server: "research") -> "Found 5 papers..."
</example>
`

	// DescriptionResumeRun is shown to the LLM for acp_resume_run.
	DescriptionResumeRun = `Resume an ACP agent run that is waiting for additional input.

<usage>
When acp_run_agent returns a response indicating the agent is "awaiting" input,
use this tool to provide the requested information and continue the run.
</usage>

<example>
acp_resume_run(run_id: "abc-123", input: "yes, proceed with the analysis") -> "Analysis complete..."
</example>
`
)

// Config defines configuration options for the ACP plugin.
type Config struct {
	Servers               []ServerConfig `json:"servers,omitempty"`
	DefaultTimeoutSeconds int            `json:"default_timeout_seconds,omitempty"`
	PollIntervalSeconds   int            `json:"poll_interval_seconds,omitempty"`
}

// ServerConfig defines a single ACP server endpoint.
type ServerConfig struct {
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Enabled *bool             `json:"enabled,omitempty"`
}

// IsEnabled returns whether this server is enabled (defaults to true).
func (s ServerConfig) IsEnabled() bool {
	if s.Enabled == nil {
		return true
	}
	return *s.Enabled
}

func init() {
	plugin.RegisterToolWithConfig(ToolListAgents, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
		mgr, err := newManager(app)
		if err != nil {
			return nil, err
		}
		return mgr.listAgentsTool(), nil
	}, &Config{})

	plugin.RegisterToolWithConfig(ToolRunAgent, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
		mgr, err := newManager(app)
		if err != nil {
			return nil, err
		}
		return mgr.runAgentTool(), nil
	}, &Config{})

	plugin.RegisterToolWithConfig(ToolResumeRun, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
		mgr, err := newManager(app)
		if err != nil {
			return nil, err
		}
		return mgr.resumeRunTool(), nil
	}, &Config{})
}

// manager holds shared state across the three ACP tools.
type manager struct {
	clients map[string]*Client
	cfg     Config
	logger  *slog.Logger
}

var (
	mgrOnce     sync.Once
	mgrInstance *manager
	mgrErr      error
)

func newManager(app *plugin.App) (*manager, error) {
	mgrOnce.Do(func() {
		var cfg Config
		if err := app.LoadConfig("acp", &cfg); err != nil {
			mgrErr = err
			return
		}
		if cfg.DefaultTimeoutSeconds <= 0 {
			cfg.DefaultTimeoutSeconds = 120
		}
		if cfg.PollIntervalSeconds <= 0 {
			cfg.PollIntervalSeconds = 2
		}

		logger := app.Logger().With("plugin", "acp")

		clients := make(map[string]*Client)
		for _, srv := range cfg.Servers {
			if !srv.IsEnabled() {
				continue
			}
			name := srv.Name
			if name == "" {
				name = srv.URL
			}
			clients[name] = NewClient(srv.URL,
				WithHeaders(srv.Headers),
				WithLogger(logger),
			)
			logger.Info("ACP server configured", "name", name, "url", srv.URL)
		}

		mgrInstance = &manager{
			clients: clients,
			cfg:     cfg,
			logger:  logger,
		}
	})
	return mgrInstance, mgrErr
}

func (m *manager) getClient(name string) (*Client, string, error) {
	if name != "" {
		c, ok := m.clients[name]
		if !ok {
			available := make([]string, 0, len(m.clients))
			for k := range m.clients {
				available = append(available, k)
			}
			return nil, "", fmt.Errorf("server %q not found, available: %s", name, strings.Join(available, ", "))
		}
		return c, name, nil
	}
	for k, c := range m.clients {
		return c, k, nil
	}
	return nil, "", fmt.Errorf("no ACP servers configured")
}

// ListAgentsParams are the parameters for acp_list_agents.
type ListAgentsParams struct {
	Server string `json:"server,omitempty" description:"Name of the ACP server to query. Uses the first configured server if omitted."`
}

func (m *manager) listAgentsTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ToolListAgents,
		DescriptionListAgents,
		func(ctx context.Context, params ListAgentsParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			client, serverName, err := m.getClient(params.Server)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			agents, err := client.ListAgents(ctx, 100, 0)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to list agents on %s: %v", serverName, err)), nil
			}

			type agentSummary struct {
				Name        string `json:"name"`
				Description string `json:"description,omitempty"`
				Server      string `json:"server"`
			}

			summaries := make([]agentSummary, len(agents))
			for i, a := range agents {
				summaries[i] = agentSummary{
					Name:        a.Name,
					Description: a.Description,
					Server:      serverName,
				}
			}

			data, _ := json.MarshalIndent(summaries, "", "  ")
			return fantasy.NewTextResponse(string(data)), nil
		},
	)
}

// RunAgentParams are the parameters for acp_run_agent.
type RunAgentParams struct {
	AgentName string `json:"agent_name" description:"Name of the remote agent to invoke."`
	Input     string `json:"input" description:"Text message to send to the agent."`
	Server    string `json:"server,omitempty" description:"Name of the ACP server. Uses the first configured server if omitted."`
	SessionID string `json:"session_id,omitempty" description:"Session ID for multi-turn conversations with the same agent."`
}

func (m *manager) runAgentTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ToolRunAgent,
		DescriptionRunAgent,
		func(ctx context.Context, params RunAgentParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.AgentName == "" {
				return fantasy.NewTextErrorResponse("agent_name is required"), nil
			}
			if params.Input == "" {
				return fantasy.NewTextErrorResponse("input is required"), nil
			}

			client, serverName, err := m.getClient(params.Server)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			m.logger.Info("invoking ACP agent",
				"agent", params.AgentName,
				"server", serverName,
			)

			input := []Message{NewUserMessage(params.Input)}

			// Try streaming first, fall back to sync.
			events, err := client.CreateRunStream(ctx, params.AgentName, input, params.SessionID)
			if err != nil {
				// Fall back to sync mode.
				m.logger.Debug("stream failed, falling back to sync", "error", err)
				return m.runSync(ctx, client, params)
			}

			return m.collectStreamResponse(events)
		},
	)
}

func (m *manager) runSync(ctx context.Context, client *Client, params RunAgentParams) (fantasy.ToolResponse, error) {
	input := []Message{NewUserMessage(params.Input)}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(m.cfg.DefaultTimeoutSeconds)*time.Second)
	defer cancel()

	run, err := client.CreateRunSync(timeoutCtx, params.AgentName, input, params.SessionID)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to run agent %s: %v", params.AgentName, err)), nil
	}

	return m.formatRunResponse(run), nil
}

func (m *manager) collectStreamResponse(events <-chan Event) (fantasy.ToolResponse, error) {
	var parts []string
	var lastRun *Run

	for event := range events {
		switch event.Type {
		case EventMessagePart:
			if event.Part != nil && event.Part.Content != "" {
				parts = append(parts, event.Part.Content)
			}
		case EventMessageCompleted:
			if event.Message != nil {
				text := TextContent([]Message{*event.Message})
				if text != "" && len(parts) == 0 {
					parts = append(parts, text)
				}
			}
		case EventRunCompleted, EventRunFailed, EventRunAwaiting, EventRunCancelled:
			lastRun = event.Run
		case EventError:
			if event.Error != nil {
				return fantasy.NewTextErrorResponse(event.Error.Message), nil
			}
		}
	}

	if lastRun != nil && lastRun.Status == RunStatusAwaiting {
		return m.formatRunResponse(lastRun), nil
	}

	if lastRun != nil && lastRun.Status == RunStatusFailed {
		msg := "agent run failed"
		if lastRun.Error != nil {
			msg = lastRun.Error.Message
		}
		return fantasy.NewTextErrorResponse(msg), nil
	}

	if len(parts) > 0 {
		return fantasy.NewTextResponse(strings.Join(parts, "")), nil
	}

	if lastRun != nil {
		return m.formatRunResponse(lastRun), nil
	}

	return fantasy.NewTextErrorResponse("no response received from agent"), nil
}

func (m *manager) formatRunResponse(run *Run) fantasy.ToolResponse {
	if run.Status == RunStatusAwaiting {
		awaitMsg := "The agent is waiting for additional input."
		if run.AwaitRequest != nil && run.AwaitRequest.Message != nil {
			awaitMsg = TextContent([]Message{*run.AwaitRequest.Message})
		}
		result := map[string]any{
			"status":  "awaiting",
			"run_id":  run.RunID,
			"message": awaitMsg,
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return fantasy.NewTextResponse(string(data))
	}

	if run.Status == RunStatusFailed {
		msg := "agent run failed"
		if run.Error != nil {
			msg = run.Error.Message
		}
		return fantasy.NewTextErrorResponse(msg)
	}

	text := TextContent(run.Output)
	if text == "" {
		data, _ := json.MarshalIndent(run, "", "  ")
		return fantasy.NewTextResponse(string(data))
	}
	return fantasy.NewTextResponse(text)
}

// ResumeRunParams are the parameters for acp_resume_run.
type ResumeRunParams struct {
	RunID  string `json:"run_id" description:"The run_id returned by acp_run_agent when the agent entered awaiting state."`
	Input  string `json:"input" description:"Text response to provide to the awaiting agent."`
	Server string `json:"server,omitempty" description:"Name of the ACP server. Uses the first configured server if omitted."`
}

func (m *manager) resumeRunTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ToolResumeRun,
		DescriptionResumeRun,
		func(ctx context.Context, params ResumeRunParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.RunID == "" {
				return fantasy.NewTextErrorResponse("run_id is required"), nil
			}
			if params.Input == "" {
				return fantasy.NewTextErrorResponse("input is required"), nil
			}

			client, _, err := m.getClient(params.Server)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			resume := &AwaitResume{
				Message: &Message{
					Role: "user",
					Parts: []MessagePart{
						{ContentType: "text/plain", Content: params.Input},
					},
				},
			}

			// Try streaming first, fall back to sync.
			events, err := client.ResumeRunStream(ctx, params.RunID, resume)
			if err != nil {
				m.logger.Debug("resume stream failed, falling back to sync", "error", err)
				run, err := client.ResumeRun(ctx, params.RunID, resume)
				if err != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to resume run: %v", err)), nil
				}
				return m.formatRunResponse(run), nil
			}

			return m.collectStreamResponse(events)
		},
	)
}

// ResetManager resets the singleton manager. Used in tests.
func ResetManager() {
	mgrOnce = sync.Once{}
	mgrInstance = nil
	mgrErr = nil
}
