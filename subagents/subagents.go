package subagents

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/plugin"
)

const (
	// ToolName is the name of the SubAgent tool.
	ToolName = "subagent"

	// Description is shown to the LLM.
	Description = `Invoke a custom sub-agent to perform a specialized task.

<usage>
- agent: The sub-agent name (e.g., "code-reviewer")
- prompt: The task for the sub-agent to perform

Use this when you need specialized expertise or want to delegate a focused task.
Each sub-agent has its own system prompt and tool access.
</usage>

<hints>
- Sub-agents run independently with their own context
- Sub-agents may have restricted tool access based on their configuration
- Results are returned as text
</hints>
`
)

// Config defines configuration options for this plugin.
type Config struct {
	Dirs []string `json:"dirs,omitempty"`
}

// DefaultDirs are searched when no dirs are configured.
var DefaultDirs = []string{".crush/agents", "~/.crush/agents"}

// SubAgentParams defines the parameters the LLM can pass.
type SubAgentParams struct {
	Agent  string `json:"agent" jsonschema:"description=The sub-agent name to invoke"`
	Prompt string `json:"prompt" jsonschema:"description=The task for the sub-agent to perform"`
}

// Registry manages loaded sub-agents.
type Registry struct {
	mu         sync.RWMutex
	agents     map[string]*SubAgent
	app        *plugin.App
	cfg        Config
	logger     *slog.Logger
	workingDir string
}

var (
	globalRegistry *Registry
	registryOnce   sync.Once
)

func getRegistry() *Registry {
	return globalRegistry
}

func init() {
	plugin.RegisterToolWithConfig(ToolName, toolFactory, &Config{})
}

func toolFactory(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
	var cfg Config
	if err := app.LoadConfig(ToolName, &cfg); err != nil {
		return nil, err
	}

	if len(cfg.Dirs) == 0 {
		cfg.Dirs = DefaultDirs
	}

	registryOnce.Do(func() {
		globalRegistry = &Registry{
			agents:     make(map[string]*SubAgent),
			app:        app,
			cfg:        cfg,
			logger:     app.Logger().With("plugin", ToolName),
			workingDir: app.WorkingDir(),
		}
		globalRegistry.LoadAgents()
	})

	return NewSubAgentTool(globalRegistry), nil
}

// LoadAgents discovers and loads all sub-agent files.
func (r *Registry) LoadAgents() {
	r.mu.Lock()
	defer r.mu.Unlock()

	files := DiscoverAgentFiles(r.cfg.Dirs, r.workingDir)
	for _, path := range files {
		agent, err := LoadAgentFile(path)
		if err != nil {
			r.logger.Warn("failed to load sub-agent", "path", path, "error", err)
			continue
		}

		// First match wins for duplicate names.
		if _, exists := r.agents[agent.Name]; !exists {
			r.agents[agent.Name] = agent
			r.logger.Debug("loaded sub-agent", "name", agent.Name, "path", path)
		}
	}
}

// Get returns a sub-agent by name.
func (r *Registry) Get(name string) (*SubAgent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agent, ok := r.agents[name]
	return agent, ok
}

// List returns all loaded sub-agents.
func (r *Registry) List() []*SubAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agents := make([]*SubAgent, 0, len(r.agents))
	for _, agent := range r.agents {
		agents = append(agents, agent)
	}
	return agents
}

// SetEnabled enables or disables a sub-agent.
func (r *Registry) SetEnabled(name string, enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if agent, ok := r.agents[name]; ok {
		agent.Enabled = enabled
	}
}

// ReloadAgent reloads a specific agent from disk.
func (r *Registry) ReloadAgent(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[name]
	if !ok {
		return fmt.Errorf("agent not found: %s", name)
	}

	newAgent, err := LoadAgentFile(agent.FilePath)
	if err != nil {
		return err
	}

	// Preserve enabled state.
	newAgent.Enabled = agent.Enabled
	r.agents[name] = newAgent
	return nil
}

// ReloadAll reloads all agents from disk.
func (r *Registry) ReloadAll() {
	r.mu.Lock()
	// Preserve enabled states.
	enabledStates := make(map[string]bool)
	for name, agent := range r.agents {
		enabledStates[name] = agent.Enabled
	}
	r.agents = make(map[string]*SubAgent)
	r.mu.Unlock()

	r.LoadAgents()

	// Restore enabled states.
	r.mu.Lock()
	for name, enabled := range enabledStates {
		if agent, ok := r.agents[name]; ok {
			agent.Enabled = enabled
		}
	}
	r.mu.Unlock()
}

// NewSubAgentTool creates the SubAgent tool.
func NewSubAgentTool(registry *Registry) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ToolName,
		buildDescription(registry),
		func(ctx context.Context, params SubAgentParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Agent == "" {
				return fantasy.NewTextErrorResponse("agent name is required"), nil
			}
			if params.Prompt == "" {
				return fantasy.NewTextErrorResponse("prompt is required"), nil
			}

			agent, ok := registry.Get(params.Agent)
			if !ok {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("sub-agent not found: %s", params.Agent)), nil
			}

			if !agent.Enabled {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("sub-agent is disabled: %s", params.Agent)), nil
			}

			runner := registry.app.SubAgentRunner()
			if runner == nil {
				return fantasy.NewTextErrorResponse("sub-agent runner not available"), nil
			}

			result, err := runner.RunSubAgent(ctx, plugin.SubAgentOptions{
				Name:            agent.Name,
				SystemPrompt:    agent.SystemPrompt,
				Prompt:          params.Prompt,
				AllowedTools:    agent.Tools,
				DisallowedTools: agent.DisallowedTools,
				Model:           agent.Model,
			})
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("sub-agent execution failed: %v", err)), nil
			}

			return fantasy.NewTextResponse(result), nil
		},
	)
}

// buildDescription creates the tool description with available agents.
func buildDescription(registry *Registry) string {
	agents := registry.List()
	if len(agents) == 0 {
		return Description + "\n<available_agents>\nNo sub-agents configured.\n</available_agents>"
	}

	var sb fmt.Stringer = &descBuilder{agents: agents}
	return Description + sb.String()
}

type descBuilder struct {
	agents []*SubAgent
}

func (d *descBuilder) String() string {
	var result string
	result = "\n<available_agents>\n"
	for _, agent := range d.agents {
		if agent.Enabled {
			result += fmt.Sprintf("- %s: %s\n", agent.Name, agent.Description)
		}
	}
	result += "</available_agents>"
	return result
}
