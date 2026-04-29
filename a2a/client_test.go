package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	a2acore "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/stretchr/testify/require"
)

func newJSONRPCServer(t *testing.T, handler func(method string, params json.RawMessage) any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      any             `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      nil,
				"error":   map[string]any{"code": -32700, "message": "parse error"},
			})
			return
		}

		result := handler(req.Method, req.Params)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		})
	}))
}

func TestClientSendMessage(t *testing.T) {
	t.Parallel()

	server := newJSONRPCServer(t, func(method string, params json.RawMessage) any {
		require.Equal(t, "SendMessage", method)
		return map[string]any{
			"task": map[string]any{
				"id":        "task-1",
				"contextId": "ctx-1",
				"status":    map[string]any{"state": "TASK_STATE_COMPLETED"},
				"artifacts": []any{
					map[string]any{
						"artifactId": "a1",
						"parts":      []any{map[string]any{"text": "echo response"}},
					},
				},
			},
		}
	})
	defer server.Close()

	client := NewClient(server.URL)
	defer client.Close()

	msg := a2acore.NewMessage(a2acore.MessageRoleUser, a2acore.NewTextPart("hello"))
	result, err := client.SendMessage(context.Background(), msg)
	require.NoError(t, err)
	require.NotNil(t, result)

	task, ok := result.(*a2acore.Task)
	require.True(t, ok, "expected *a2a.Task, got %T", result)
	require.Equal(t, a2acore.TaskID("task-1"), task.ID)
	require.Equal(t, a2acore.TaskStateCompleted, task.Status.State)
}

func TestClientGetTask(t *testing.T) {
	t.Parallel()

	server := newJSONRPCServer(t, func(method string, params json.RawMessage) any {
		require.Equal(t, "GetTask", method)
		return map[string]any{
			"id":        "task-2",
			"contextId": "ctx-2",
			"status":    map[string]any{"state": "TASK_STATE_COMPLETED"},
		}
	})
	defer server.Close()

	client := NewClient(server.URL)
	defer client.Close()

	task, err := client.GetTask(context.Background(), "task-2")
	require.NoError(t, err)
	require.Equal(t, a2acore.TaskID("task-2"), task.ID)
}

func TestClientCancelTask(t *testing.T) {
	t.Parallel()

	server := newJSONRPCServer(t, func(method string, params json.RawMessage) any {
		require.Equal(t, "CancelTask", method)
		return map[string]any{
			"id":        "task-3",
			"contextId": "ctx-3",
			"status":    map[string]any{"state": "TASK_STATE_CANCELED"},
		}
	})
	defer server.Close()

	client := NewClient(server.URL)
	defer client.Close()

	task, err := client.CancelTask(context.Background(), "task-3")
	require.NoError(t, err)
	require.Equal(t, a2acore.TaskStateCanceled, task.Status.State)
}

func TestClientSendMessageWithContext(t *testing.T) {
	t.Parallel()

	server := newJSONRPCServer(t, func(method string, params json.RawMessage) any {
		var p struct {
			Message struct {
				ContextID string `json:"contextId"`
			} `json:"message"`
		}
		json.Unmarshal(params, &p)
		require.Equal(t, "ctx-existing", p.Message.ContextID)
		return map[string]any{
			"task": map[string]any{
				"id":        "task-4",
				"contextId": "ctx-existing",
				"status":    map[string]any{"state": "TASK_STATE_COMPLETED"},
			},
		}
	})
	defer server.Close()

	client := NewClient(server.URL)
	defer client.Close()

	result, err := client.SendMessageWithContext(context.Background(), "follow-up", "ctx-existing")
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestClientCachesConnection(t *testing.T) {
	t.Parallel()

	callCount := 0
	server := newJSONRPCServer(t, func(method string, params json.RawMessage) any {
		callCount++
		return map[string]any{
			"task": map[string]any{
				"id":        "task-cache",
				"contextId": "ctx-cache",
				"status":    map[string]any{"state": "TASK_STATE_COMPLETED"},
			},
		}
	})
	defer server.Close()

	client := NewClient(server.URL)
	defer client.Close()

	msg := a2acore.NewMessage(a2acore.MessageRoleUser, a2acore.NewTextPart("test"))
	_, err := client.SendMessage(context.Background(), msg)
	require.NoError(t, err)

	_, err = client.SendMessage(context.Background(), msg)
	require.NoError(t, err)

	require.Equal(t, 2, callCount)
}

func TestClientClose(t *testing.T) {
	t.Parallel()

	server := newJSONRPCServer(t, func(method string, params json.RawMessage) any {
		return map[string]any{
			"task": map[string]any{
				"id":        "task-close",
				"contextId": "ctx-close",
				"status":    map[string]any{"state": "TASK_STATE_COMPLETED"},
			},
		}
	})
	defer server.Close()

	client := NewClient(server.URL)
	msg := a2acore.NewMessage(a2acore.MessageRoleUser, a2acore.NewTextPart("test"))
	_, err := client.SendMessage(context.Background(), msg)
	require.NoError(t, err)

	require.NoError(t, client.Close())
	require.NoError(t, client.Close())
}
