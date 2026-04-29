package a2a

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/plugin"
	"github.com/stretchr/testify/require"
)

func TestListAgentsTool(t *testing.T) {
	t.Parallel()

	server := newJSONRPCServer(t, func(method string, params json.RawMessage) any {
		return map[string]any{
			"task": map[string]any{
				"id": "task-list", "contextId": "ctx-list",
				"status": map[string]any{"state": "TASK_STATE_COMPLETED"},
			},
		}
	})
	defer server.Close()

	mgr := newTestManager(t, server.URL)
	tool := mgr.listAgentsTool()
	require.Equal(t, ToolListAgents, tool.Info().Name)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID: "test-1", Name: ToolListAgents, Input: `{}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "test")
}

func TestSendMessageTool(t *testing.T) {
	t.Parallel()

	server := newJSONRPCServer(t, func(method string, params json.RawMessage) any {
		return map[string]any{
			"task": map[string]any{
				"id": "task-send", "contextId": "ctx-send",
				"status": map[string]any{"state": "TASK_STATE_COMPLETED"},
				"artifacts": []any{
					map[string]any{
						"artifactId": "a1",
						"parts":      []any{map[string]any{"text": "echo: hello world"}},
					},
				},
			},
		}
	})
	defer server.Close()

	mgr := newTestManager(t, server.URL)
	tool := mgr.sendMessageTool()
	require.Equal(t, ToolSendMessage, tool.Info().Name)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID: "test-2", Name: ToolSendMessage, Input: `{"input": "hello world"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "echo: hello world")
}

func TestSendMessageToolMissingInput(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t, "http://unused")
	tool := mgr.sendMessageTool()

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID: "test-3", Name: ToolSendMessage, Input: `{}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "input is required")
}

func TestSendMessageToolNoServer(t *testing.T) {
	t.Parallel()

	mgr := &manager{
		clients: make(map[string]*Client),
		cfg:     Config{DefaultTimeoutSeconds: 5},
		logger:  slog.Default(),
	}
	tool := mgr.sendMessageTool()

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID: "test-4", Name: ToolSendMessage, Input: `{"input": "hello"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "no A2A servers configured")
}

func TestGetTaskTool(t *testing.T) {
	t.Parallel()

	server := newJSONRPCServer(t, func(method string, params json.RawMessage) any {
		require.Equal(t, "GetTask", method)
		return map[string]any{
			"id": "task-get", "contextId": "ctx-get",
			"status": map[string]any{"state": "TASK_STATE_COMPLETED"},
		}
	})
	defer server.Close()

	mgr := newTestManager(t, server.URL)
	tool := mgr.getTaskTool()
	require.Equal(t, ToolGetTask, tool.Info().Name)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID: "test-5", Name: ToolGetTask, Input: `{"task_id": "task-get"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
}

func TestGetTaskToolMissingID(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t, "http://unused")
	tool := mgr.getTaskTool()

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID: "test-6", Name: ToolGetTask, Input: `{}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "task_id is required")
}

func TestAttachFileTool(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("file content"), 0o644))

	app := plugin.NewApp(plugin.WithLogger(nil))
	hook, err := NewServerHook(app, A2AServerConfig{})
	require.NoError(t, err)

	hook.taskCtxMu.Lock()
	hook.currentTask = "active-task-1"
	hook.taskCtxMu.Unlock()

	mgr := newTestManager(t, "http://unused")
	mgr.serverHook = hook
	tool := mgr.attachFileTool()
	require.Equal(t, ToolAttachFile, tool.Info().Name)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID: "test-7", Name: ToolAttachFile,
		Input: `{"file_path": "` + filePath + `", "name": "Test File"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Artifact")
	require.Contains(t, resp.Content, "Test File")

	arts := hook.ArtifactStore().Get("active-task-1")
	require.Len(t, arts, 1)
	require.Equal(t, "Test File", arts[0].Name)
}

func TestAttachFileToolNoServer(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t, "http://unused")
	tool := mgr.attachFileTool()

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID: "test-8", Name: ToolAttachFile, Input: `{"file_path": "/tmp/x.txt"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "only works when Crush is processing")
}

func TestAttachFileToolNoActiveTask(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp(plugin.WithLogger(nil))
	hook, err := NewServerHook(app, A2AServerConfig{})
	require.NoError(t, err)

	mgr := newTestManager(t, "http://unused")
	mgr.serverHook = hook
	tool := mgr.attachFileTool()

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID: "test-9", Name: ToolAttachFile, Input: `{"file_path": "/tmp/x.txt"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "no active A2A task")
}

func TestAttachFileToolMissingPath(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t, "http://unused")
	tool := mgr.attachFileTool()

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID: "test-10", Name: ToolAttachFile, Input: `{}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "file_path is required")
}

func newTestManager(t *testing.T, serverURL string) *manager {
	t.Helper()
	return &manager{
		clients: map[string]*Client{"test": NewClient(serverURL)},
		cfg:     Config{DefaultTimeoutSeconds: 10},
		logger:  slog.Default(),
	}
}

func init() {
	mgrOnce = sync.Once{}
	mgrInstance = nil
	mgrErr = nil
}
