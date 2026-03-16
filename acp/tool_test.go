package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func TestListAgentsTool(t *testing.T) {
	t.Parallel()

	server := newMockACPServer(t, []AgentManifest{
		{Name: "echo", Description: "Echoes input"},
		{Name: "summarizer", Description: "Summarizes text"},
	})
	defer server.Close()

	mgr := newTestManager(t, server.URL)
	tool := mgr.listAgentsTool()

	require.Equal(t, ToolListAgents, tool.Info().Name)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "test-1",
		Name:  ToolListAgents,
		Input: `{}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "echo")
	require.Contains(t, resp.Content, "summarizer")
}

func TestRunAgentToolSync(t *testing.T) {
	t.Parallel()

	server := newEchoACPServer(t)
	defer server.Close()

	mgr := newTestManager(t, server.URL)
	tool := mgr.runAgentTool()

	require.Equal(t, ToolRunAgent, tool.Info().Name)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "test-2",
		Name:  ToolRunAgent,
		Input: `{"agent_name": "echo", "input": "hello world"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "hello world")
}

func TestRunAgentToolStream(t *testing.T) {
	t.Parallel()

	server := newStreamACPServer(t)
	defer server.Close()

	mgr := newTestManager(t, server.URL)
	tool := mgr.runAgentTool()

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "test-3",
		Name:  ToolRunAgent,
		Input: `{"agent_name": "echo", "input": "hi"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Hello World")
}

func TestRunAgentToolMissingParams(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t, "http://unused")
	tool := mgr.runAgentTool()

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "test-4",
		Name:  ToolRunAgent,
		Input: `{}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "agent_name is required")
}

func TestRunAgentToolNoServer(t *testing.T) {
	t.Parallel()

	mgr := &manager{
		clients: make(map[string]*Client),
		cfg:     Config{DefaultTimeoutSeconds: 5},
		logger:  slog.Default(),
	}
	tool := mgr.runAgentTool()

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "test-5",
		Name:  ToolRunAgent,
		Input: `{"agent_name": "echo", "input": "hello"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "no ACP servers configured")
}

func TestResumeRunTool(t *testing.T) {
	t.Parallel()

	server := newResumeACPServer(t)
	defer server.Close()

	mgr := newTestManager(t, server.URL)
	tool := mgr.resumeRunTool()

	require.Equal(t, ToolResumeRun, tool.Info().Name)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "test-6",
		Name:  ToolResumeRun,
		Input: `{"run_id": "run-await-1", "input": "yes, proceed"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Approved")
}

func TestResumeRunToolMissingParams(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t, "http://unused")
	tool := mgr.resumeRunTool()

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "test-7",
		Name:  ToolResumeRun,
		Input: `{"run_id": ""}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "run_id is required")
}

func TestRunAgentToolAwaitingResponse(t *testing.T) {
	t.Parallel()

	server := newAwaitACPServer(t)
	defer server.Close()

	mgr := newTestManager(t, server.URL)
	tool := mgr.runAgentTool()

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "test-8",
		Name:  ToolRunAgent,
		Input: `{"agent_name": "approval", "input": "please approve"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "awaiting")
	require.Contains(t, resp.Content, "run-await-1")
}

func TestServerConfigEnabled(t *testing.T) {
	t.Parallel()

	enabled := true
	disabled := false

	require.True(t, ServerConfig{Enabled: nil}.IsEnabled())
	require.True(t, ServerConfig{Enabled: &enabled}.IsEnabled())
	require.False(t, ServerConfig{Enabled: &disabled}.IsEnabled())
}

// Test helpers.

func newTestManager(t *testing.T, serverURL string) *manager {
	t.Helper()
	return &manager{
		clients: map[string]*Client{
			"test": NewClient(serverURL),
		},
		cfg:    Config{DefaultTimeoutSeconds: 10, PollIntervalSeconds: 1},
		logger: slog.Default(),
	}
}

func newMockACPServer(t *testing.T, agents []AgentManifest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(AgentsListResponse{Agents: agents})
	}))
}

func newEchoACPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/runs" && r.Method == http.MethodPost {
			var req RunCreateRequest
			json.NewDecoder(r.Body).Decode(&req)

			if req.Mode == RunModeStream {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(ACPError{Message: "stream not supported"})
				return
			}

			json.NewEncoder(w).Encode(Run{
				AgentName: req.AgentName,
				RunID:     "run-echo-1",
				Status:    RunStatusCompleted,
				Output:    []Message{NewAgentMessage("Echo: " + TextContent(req.Input))},
			})
		}
	}))
}

func newStreamACPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		events := []string{
			`{"type":"run.in-progress","run":{"agent_name":"echo","run_id":"r1","status":"in-progress","output":[],"created_at":"2025-01-01T00:00:00Z"}}`,
			`{"type":"message.part","part":{"content_type":"text/plain","content":"Hello"}}`,
			`{"type":"message.part","part":{"content_type":"text/plain","content":" World"}}`,
			`{"type":"run.completed","run":{"agent_name":"echo","run_id":"r1","status":"completed","output":[],"created_at":"2025-01-01T00:00:00Z"}}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
		}
	}))
}

func newAwaitACPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		events := []string{
			`{"type":"run.awaiting","run":{"agent_name":"approval","run_id":"run-await-1","status":"awaiting","output":[],"await_request":{"message":{"role":"agent","parts":[{"content_type":"text/plain","content":"Do you approve?"}]}},"created_at":"2025-01-01T00:00:00Z"}}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
		}
	}))
}

func newResumeACPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Resume falls back to sync since stream returns error.
			var req RunResumeRequest
			json.NewDecoder(r.Body).Decode(&req)

			if req.Mode == RunModeStream {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(ACPError{Message: "stream not supported"})
				return
			}

			json.NewEncoder(w).Encode(Run{
				AgentName: "approval",
				RunID:     req.RunID,
				Status:    RunStatusCompleted,
				Output:    []Message{NewAgentMessage("Approved and processed")},
			})
		}
	}))
}

// Ensure the singleton doesn't leak between tests.
func init() {
	mgrOnce = sync.Once{}
	mgrInstance = nil
	mgrErr = nil
}
