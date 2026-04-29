package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"

	a2acore "github.com/a2aproject/a2a-go/v2/a2a"
	"charm.land/fantasy"
	"github.com/charmbracelet/crush/plugin"
)

const (
	ToolListAgents  = "a2a_list_agents"
	ToolSendMessage = "a2a_send_message"
	ToolGetTask     = "a2a_get_task"
	ToolAttachFile  = "a2a_attach_file"

	DescriptionListAgents = `List available remote agents on A2A servers.

<usage>
Use this tool to discover what remote AI agents are available before invoking them.
Fetches the agent card from each configured A2A server.
Returns agent names, descriptions, skills, and capabilities.
</usage>

<example>
a2a_list_agents() -> [{"name": "summarizer", "description": "Summarizes documents", "skills": [...]}]
a2a_list_agents(server: "research") -> agents from specific server
</example>
`

	DescriptionSendMessage = `Send a message to a remote A2A agent and get its response.

<usage>
Send a text message to a remote A2A agent. The agent processes it and returns a Task
with artifacts, or a direct Message response.

Use a2a_list_agents first to discover available agents.
Use context_id from a previous response to continue a multi-turn conversation.
</usage>

<example>
a2a_send_message(input: "Summarize this document: ...", server: "research") -> {"task_id": "...", "status": "completed", "response": "..."}
a2a_send_message(input: "Follow up question", context_id: "ctx-123") -> continues previous conversation
</example>
`

	DescriptionGetTask = `Get the status and artifacts of a previously created A2A task.

<usage>
Retrieve the current state of a task by its ID. Returns the task status,
any artifacts produced, and conversation history.
</usage>

<example>
a2a_get_task(task_id: "abc-123") -> {"status": "completed", "artifacts": [...]}
</example>
`

	DescriptionAttachFile = `Attach a file as an A2A artifact to the current task response.

<usage>
When Crush is running as an A2A server and processing a request from a remote agent,
use this tool to attach files as artifacts in the response. The file will be included
as a base64-encoded artifact in the task result.

Only works when processing an incoming A2A request.
</usage>

<example>
a2a_attach_file(file_path: "/path/to/report.pdf", name: "Analysis Report") -> "Artifact attached"
a2a_attach_file(file_path: "output.json", media_type: "application/json") -> "Artifact attached"
</example>
`
)

func init() {
	plugin.RegisterToolWithConfig(ToolListAgents, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
		mgr, err := getManager(app)
		if err != nil {
			return nil, err
		}
		return mgr.listAgentsTool(), nil
	}, &Config{})

	plugin.RegisterToolWithConfig(ToolSendMessage, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
		mgr, err := getManager(app)
		if err != nil {
			return nil, err
		}
		return mgr.sendMessageTool(), nil
	}, &Config{})

	plugin.RegisterToolWithConfig(ToolGetTask, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
		mgr, err := getManager(app)
		if err != nil {
			return nil, err
		}
		return mgr.getTaskTool(), nil
	}, &Config{})

	plugin.RegisterToolWithConfig(ToolAttachFile, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
		mgr, err := getManager(app)
		if err != nil {
			return nil, err
		}
		return mgr.attachFileTool(), nil
	}, &Config{})
}

type manager struct {
	clients    map[string]*Client
	cfg        Config
	logger     *slog.Logger
	serverHook *ServerHook
}

var (
	mgrOnce     sync.Once
	mgrInstance *manager
	mgrErr      error
)

func getManager(app *plugin.App) (*manager, error) {
	mgrOnce.Do(func() {
		var cfg Config
		if err := app.LoadConfig(PluginName, &cfg); err != nil {
			mgrErr = err
			return
		}
		if cfg.DefaultTimeoutSeconds <= 0 {
			cfg.DefaultTimeoutSeconds = 120
		}

		logger := app.Logger().With("plugin", PluginName)

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
			logger.Info("A2A server configured", "name", name, "url", srv.URL)
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
	return nil, "", fmt.Errorf("no A2A servers configured")
}

type ListAgentsParams struct {
	Server string `json:"server,omitempty" jsonschema:"description=Name of the A2A server to query. Uses the first configured server if omitted."`
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

			msg := a2acore.NewMessage(a2acore.MessageRoleUser, a2acore.NewTextPart("list capabilities"))
			result, err := client.SendMessage(ctx, msg)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to contact %s: %v", serverName, err)), nil
			}

			type agentSummary struct {
				Name     string `json:"name"`
				Server   string `json:"server"`
				TaskID   string `json:"task_id,omitempty"`
				Response string `json:"response,omitempty"`
			}

			summary := agentSummary{
				Name:   serverName,
				Server: serverName,
			}

			switch v := result.(type) {
			case *a2acore.Task:
				summary.TaskID = string(v.ID)
				if len(v.Artifacts) > 0 {
					summary.Response = ExtractTextFromParts(v.Artifacts[0].Parts)
				}
			case *a2acore.Message:
				summary.Response = ExtractText(v)
			}

			data, _ := json.MarshalIndent(summary, "", "  ")
			return fantasy.NewTextResponse(string(data)), nil
		},
	)
}

type SendMessageParams struct {
	Input     string `json:"input" jsonschema:"description=Text message to send to the agent."`
	Server    string `json:"server,omitempty" jsonschema:"description=Name of the A2A server. Uses the first configured server if omitted."`
	ContextID string `json:"context_id,omitempty" jsonschema:"description=Context ID from a previous response for multi-turn conversations."`
}

func (m *manager) sendMessageTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ToolSendMessage,
		DescriptionSendMessage,
		func(ctx context.Context, params SendMessageParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Input == "" {
				return fantasy.NewTextErrorResponse("input is required"), nil
			}

			client, serverName, err := m.getClient(params.Server)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			m.logger.Info("sending A2A message",
				"server", serverName,
			)

			var result a2acore.SendMessageResult
			if params.ContextID != "" {
				result, err = client.SendMessageWithContext(ctx, params.Input, params.ContextID)
			} else {
				msg := a2acore.NewMessage(a2acore.MessageRoleUser, a2acore.NewTextPart(params.Input))
				result, err = client.SendMessage(ctx, msg)
			}

			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to send message to %s: %v", serverName, err)), nil
			}

			return formatResult(result), nil
		},
	)
}

type GetTaskParams struct {
	TaskID string `json:"task_id" jsonschema:"description=The task ID to retrieve."`
	Server string `json:"server,omitempty" jsonschema:"description=Name of the A2A server. Uses the first configured server if omitted."`
}

func (m *manager) getTaskTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ToolGetTask,
		DescriptionGetTask,
		func(ctx context.Context, params GetTaskParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.TaskID == "" {
				return fantasy.NewTextErrorResponse("task_id is required"), nil
			}

			client, _, err := m.getClient(params.Server)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			task, err := client.GetTask(ctx, a2acore.TaskID(params.TaskID))
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to get task: %v", err)), nil
			}

			return formatTask(task), nil
		},
	)
}

type AttachFileParams struct {
	FilePath    string `json:"file_path" jsonschema:"description=Path to the file to attach."`
	Name        string `json:"name,omitempty" jsonschema:"description=Human-readable name for the artifact."`
	Description string `json:"description,omitempty" jsonschema:"description=Description of the artifact."`
	MediaType   string `json:"media_type,omitempty" jsonschema:"description=MIME type. Auto-detected from extension if omitted."`
}

func (m *manager) attachFileTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ToolAttachFile,
		DescriptionAttachFile,
		func(ctx context.Context, params AttachFileParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}

			if m.serverHook == nil {
				return fantasy.NewTextErrorResponse("a2a_attach_file only works when Crush is processing an incoming A2A request"), nil
			}

			taskID := m.serverHook.CurrentTaskID()
			if taskID == "" {
				return fantasy.NewTextErrorResponse("no active A2A task — this tool only works during A2A request processing"), nil
			}

			data, err := os.ReadFile(params.FilePath)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to read file: %v", err)), nil
			}

			mediaType := params.MediaType
			if mediaType == "" {
				mediaType = mime.TypeByExtension(filepath.Ext(params.FilePath))
				if mediaType == "" {
					mediaType = "application/octet-stream"
				}
			}

			name := params.Name
			if name == "" {
				name = filepath.Base(params.FilePath)
			}

			part := a2acore.NewRawPart(data)
			part.MediaType = mediaType
			part.Filename = filepath.Base(params.FilePath)

			artifact := &a2acore.Artifact{
				ID:          a2acore.NewArtifactID(),
				Name:        name,
				Description: params.Description,
				Parts:       a2acore.ContentParts{part},
			}

			m.serverHook.ArtifactStore().Add(taskID, artifact)

			return fantasy.NewTextResponse(fmt.Sprintf("Artifact %q attached to task %s", name, taskID)), nil
		},
	)
}

func formatResult(result a2acore.SendMessageResult) fantasy.ToolResponse {
	switch v := result.(type) {
	case *a2acore.Task:
		return formatTask(v)
	case *a2acore.Message:
		text := ExtractText(v)
		if text != "" {
			return fantasy.NewTextResponse(text)
		}
		data, _ := json.MarshalIndent(v, "", "  ")
		return fantasy.NewTextResponse(string(data))
	default:
		data, _ := json.MarshalIndent(result, "", "  ")
		return fantasy.NewTextResponse(string(data))
	}
}

func formatTask(task *a2acore.Task) fantasy.ToolResponse {
	if task.Status.State == a2acore.TaskStateFailed {
		msg := "task failed"
		if task.Status.Message != nil {
			msg = ExtractText(task.Status.Message)
		}
		return fantasy.NewTextErrorResponse(msg)
	}

	if task.Status.State == a2acore.TaskStateInputRequired {
		result := map[string]any{
			"status":     "input_required",
			"task_id":    string(task.ID),
			"context_id": task.ContextID,
		}
		if task.Status.Message != nil {
			result["message"] = ExtractText(task.Status.Message)
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return fantasy.NewTextResponse(string(data))
	}

	var texts []string
	for _, art := range task.Artifacts {
		text := ExtractTextFromParts(art.Parts)
		if text != "" {
			texts = append(texts, text)
		}
	}

	if len(texts) > 0 {
		result := map[string]any{
			"task_id":    string(task.ID),
			"context_id": task.ContextID,
			"status":     string(task.Status.State),
			"response":   strings.Join(texts, "\n"),
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return fantasy.NewTextResponse(string(data))
	}

	data, _ := json.MarshalIndent(task, "", "  ")
	return fantasy.NewTextResponse(string(data))
}

// ResetManager resets the singleton manager for testing.
func ResetManager() {
	mgrOnce = sync.Once{}
	mgrInstance = nil
	mgrErr = nil
}
