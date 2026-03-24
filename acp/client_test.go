package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClientListAgents(t *testing.T) {
	t.Parallel()

	agents := []AgentManifest{
		{Name: "echo", Description: "Echoes input"},
		{Name: "summarizer", Description: "Summarizes text"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/agents", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		json.NewEncoder(w).Encode(AgentsListResponse{Agents: agents})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	result, err := client.ListAgents(context.Background(), 10, 0)
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, "echo", result[0].Name)
	require.Equal(t, "summarizer", result[1].Name)
}

func TestClientGetAgent(t *testing.T) {
	t.Parallel()

	manifest := AgentManifest{
		Name:        "echo",
		Description: "Echoes input back",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/agents/echo", r.URL.Path)
		json.NewEncoder(w).Encode(manifest)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	result, err := client.GetAgent(context.Background(), "echo")
	require.NoError(t, err)
	require.Equal(t, "echo", result.Name)
}

func TestClientCreateRunSync(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/runs", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var req RunCreateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "echo", req.AgentName)
		require.Equal(t, RunModeSync, req.Mode)

		run := Run{
			AgentName: "echo",
			RunID:     "run-1",
			Status:    RunStatusCompleted,
			Output:    []Message{NewAgentMessage("Echo: " + TextContent(req.Input))},
			CreatedAt: time.Now(),
		}
		json.NewEncoder(w).Encode(run)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	run, err := client.CreateRunSync(context.Background(), "echo", []Message{NewUserMessage("hello")}, "")
	require.NoError(t, err)
	require.Equal(t, RunStatusCompleted, run.Status)
	require.Equal(t, "Echo: hello", TextContent(run.Output))
}

func TestClientCreateRunAsync(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/runs" && r.Method == http.MethodPost {
			var req RunCreateRequest
			json.NewDecoder(r.Body).Decode(&req)
			require.Equal(t, RunModeAsync, req.Mode)

			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(Run{
				AgentName: "slow",
				RunID:     "run-async-1",
				Status:    RunStatusCreated,
				Output:    []Message{},
				CreatedAt: time.Now(),
			})
			return
		}
		if r.URL.Path == "/runs/run-async-1" && r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(Run{
				AgentName: "slow",
				RunID:     "run-async-1",
				Status:    RunStatusCompleted,
				Output:    []Message{NewAgentMessage("done")},
				CreatedAt: time.Now(),
			})
			return
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	run, err := client.CreateRunAsync(context.Background(), "slow", []Message{NewUserMessage("work")}, "")
	require.NoError(t, err)
	require.Equal(t, "run-async-1", run.RunID)

	final, err := client.PollRun(context.Background(), "run-async-1", 100*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, RunStatusCompleted, final.Status)
}

func TestClientCreateRunStream(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/runs", r.URL.Path)

		w.Header().Set("Content-Type", ContentTypeNDJSON)
		w.WriteHeader(http.StatusOK)

		events := []string{
			`{"type":"run.created","run":{"agent_name":"echo","run_id":"r1","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}`,
			`{"type":"message.part","part":{"content_type":"text/plain","content":"Hello"}}`,
			`{"type":"message.part","part":{"content_type":"text/plain","content":" World"}}`,
			`{"type":"run.completed","run":{"agent_name":"echo","run_id":"r1","status":"completed","output":[],"created_at":"2025-01-01T00:00:00Z"}}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "%s\n", e)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ch, err := client.CreateRunStream(context.Background(), "echo", []Message{NewUserMessage("hi")}, "")
	require.NoError(t, err)

	var collected []Event
	for e := range ch {
		collected = append(collected, e)
	}
	require.Len(t, collected, 4)
	require.Equal(t, EventMessagePart, collected[1].Type)
	require.Equal(t, "Hello", collected[1].Part.Content)
}

func TestClientResumeRun(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/runs/run-await-1", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var req RunResumeRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "run-await-1", req.RunID)

		json.NewEncoder(w).Encode(Run{
			AgentName: "approval",
			RunID:     "run-await-1",
			Status:    RunStatusCompleted,
			Output:    []Message{NewAgentMessage("Approved and processed")},
			CreatedAt: time.Now(),
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	resume := &AwaitResume{Message: &Message{
		Role:  "user",
		Parts: []MessagePart{{ContentType: "text/plain", Content: "yes"}},
	}}
	run, err := client.ResumeRun(context.Background(), "run-await-1", resume)
	require.NoError(t, err)
	require.Equal(t, RunStatusCompleted, run.Status)
}

func TestClientCancelRun(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/runs/run-cancel-1/cancel", r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(Run{
			AgentName: "slow",
			RunID:     "run-cancel-1",
			Status:    RunStatusCancelling,
			Output:    []Message{},
			CreatedAt: time.Now(),
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	run, err := client.CancelRun(context.Background(), "run-cancel-1")
	require.NoError(t, err)
	require.Equal(t, RunStatusCancelling, run.Status)
}

func TestClientHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ACPError{Code: 500, Message: "internal server error"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.ListAgents(context.Background(), 10, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "internal server error")
}

func TestClientCustomHeaders(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode(AgentsListResponse{Agents: []AgentManifest{}})
	}))
	defer server.Close()

	client := NewClient(server.URL, WithHeaders(map[string]string{
		"Authorization": "Bearer test-token",
	}))
	_, err := client.ListAgents(context.Background(), 10, 0)
	require.NoError(t, err)
}

func TestClientPollRunTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Run{
			AgentName: "slow",
			RunID:     "run-forever",
			Status:    RunStatusInProgress,
			Output:    []Message{},
			CreatedAt: time.Now(),
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := client.PollRun(ctx, "run-forever", 50*time.Millisecond)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
