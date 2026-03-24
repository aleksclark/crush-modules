package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Ping ---

func TestPing(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "pong")
	}))
	defer server.Close()

	client := NewClient(server.URL)
	require.NoError(t, client.Ping(context.Background()))
}

func TestPingError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "not ready")
	}))
	defer server.Close()

	client := NewClient(server.URL)
	err := client.Ping(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected response")
}

// --- ListAgents ---

func TestListAgents(t *testing.T) {
	t.Parallel()

	agents := []AgentManifest{
		{Name: "crush", Description: "Crush AI assistant"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/agents", r.URL.Path)
		json.NewEncoder(w).Encode(agentsListResponse{Agents: agents})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	result, err := client.ListAgents(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "crush", result[0].Name)
}

// --- NewSession ---

func TestNewSession(t *testing.T) {
	t.Parallel()

	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agents" {
			json.NewEncoder(w).Encode(agentsListResponse{
				Agents: []AgentManifest{{Name: "crush"}},
			})
			return
		}
		require.Equal(t, "/runs", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var req runCreateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "crush", req.AgentName)
		require.Equal(t, RunModeSync, req.Mode)
		require.Empty(t, req.SessionID)
		require.Len(t, req.Input, 1)
		require.Equal(t, "hello", req.Input[0].Parts[0].Content)

		json.NewEncoder(w).Encode(Run{
			AgentName: "crush",
			RunID:     "run-1",
			SessionID: "ses-abc",
			Status:    RunStatusCompleted,
			Output:    []Message{NewAgentMessage("Hi there!")},
			CreatedAt: time.Now(),
		})
	})
	defer server.Close()

	client := NewClient(server.URL)
	result, err := client.NewSession(context.Background(), "hello")
	require.NoError(t, err)
	require.Equal(t, "ses-abc", result.Run.SessionID)
	require.Equal(t, RunStatusCompleted, result.Run.Status)
	require.Equal(t, "Hi there!", result.Text())
}

// --- Resume ---

func TestResume(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agents" {
			json.NewEncoder(w).Encode(agentsListResponse{
				Agents: []AgentManifest{{Name: "crush"}},
			})
			return
		}

		count := callCount.Add(1)
		require.Equal(t, "/runs", r.URL.Path)

		var req runCreateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		if count == 1 {
			require.Empty(t, req.SessionID)
			json.NewEncoder(w).Encode(Run{
				AgentName: "crush",
				RunID:     "run-1",
				SessionID: "ses-abc",
				Status:    RunStatusCompleted,
				Output:    []Message{NewAgentMessage("Looking at auth.go...")},
				CreatedAt: time.Now(),
			})
		} else {
			require.Equal(t, "ses-abc", req.SessionID)
			json.NewEncoder(w).Encode(Run{
				AgentName: "crush",
				RunID:     "run-2",
				SessionID: "ses-abc",
				Status:    RunStatusCompleted,
				Output:    []Message{NewAgentMessage("Fixed the bug.")},
				CreatedAt: time.Now(),
			})
		}
	})
	defer server.Close()

	client := NewClient(server.URL)

	first, err := client.NewSession(context.Background(), "look at auth.go")
	require.NoError(t, err)
	require.Equal(t, "ses-abc", first.Run.SessionID)

	second, err := client.Resume(context.Background(), first.Run.SessionID, "fix it")
	require.NoError(t, err)
	require.Equal(t, "ses-abc", second.Run.SessionID)
	require.Equal(t, "Fixed the bug.", second.Text())
}

func TestResumeRequiresSessionID(t *testing.T) {
	t.Parallel()

	client := NewClient("http://localhost:9999")
	_, err := client.Resume(context.Background(), "", "hello")
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID is required")
}

// --- Streaming ---

func TestNewSessionStream(t *testing.T) {
	t.Parallel()

	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agents" {
			json.NewEncoder(w).Encode(agentsListResponse{
				Agents: []AgentManifest{{Name: "crush"}},
			})
			return
		}

		require.Equal(t, "application/x-ndjson", r.Header.Get("Accept"))

		var req runCreateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, RunModeStream, req.Mode)

		w.Header().Set("Content-Type", contentTypeNDJSON)
		w.WriteHeader(http.StatusOK)

		events := []string{
			`{"type":"run.created","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}`,
			`{"type":"run.in-progress","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"in-progress","output":[],"created_at":"2025-01-01T00:00:00Z"}}`,
			`{"type":"message.part","part":{"content_type":"text/plain","content":"Hello"}}`,
			`{"type":"message.part","part":{"content_type":"text/plain","content":" World"}}`,
			`{"type":"run.completed","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"completed","output":[{"role":"agent","parts":[{"content_type":"text/plain","content":"Hello World"}]}],"created_at":"2025-01-01T00:00:00Z"}}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "%s\n", e)
		}
	})
	defer server.Close()

	client := NewClient(server.URL)
	stream, err := client.NewSessionStream(context.Background(), "hi")
	require.NoError(t, err)

	var parts []string
	var eventTypes []EventType
	for ev := range stream.Events {
		eventTypes = append(eventTypes, ev.Type)
		if ev.Type == EventMessagePart && ev.Part != nil {
			parts = append(parts, ev.Part.Content)
		}
	}

	require.NoError(t, stream.Err())
	require.Equal(t, []string{"Hello", " World"}, parts)
	require.Contains(t, eventTypes, EventRunCreated)
	require.Contains(t, eventTypes, EventRunCompleted)
}

func TestStreamResult(t *testing.T) {
	t.Parallel()

	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agents" {
			json.NewEncoder(w).Encode(agentsListResponse{
				Agents: []AgentManifest{{Name: "crush"}},
			})
			return
		}

		w.Header().Set("Content-Type", contentTypeNDJSON)
		w.WriteHeader(http.StatusOK)

		events := []string{
			`{"type":"run.created","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}`,
			`{"type":"run.completed","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-1","status":"completed","output":[{"role":"agent","parts":[{"content_type":"text/plain","content":"done"}]}],"created_at":"2025-01-01T00:00:00Z"}}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "%s\n", e)
		}
	})
	defer server.Close()

	client := NewClient(server.URL)
	stream, err := client.NewSessionStream(context.Background(), "hi")
	require.NoError(t, err)

	result, err := stream.Result()
	require.NoError(t, err)
	require.NotNil(t, result.Run)
	require.Equal(t, "ses-1", result.Run.SessionID)
	require.Equal(t, RunStatusCompleted, result.Run.Status)
}

func TestResumeStreamRequiresSessionID(t *testing.T) {
	t.Parallel()

	client := NewClient("http://localhost:9999")
	_, err := client.ResumeStream(context.Background(), "", "hello")
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID is required")
}

// --- Dump ---

func TestDump(t *testing.T) {
	t.Parallel()

	snapshot := SessionSnapshot{
		Version: 1,
		Session: SessionData{
			ID:           "ses-abc",
			Title:        "Fix auth bug",
			MessageCount: 4,
			CreatedAt:    1700000000,
			UpdatedAt:    1700000120,
		},
		Messages: []SessionMessage{
			{
				ID:        "msg-1",
				SessionID: "ses-abc",
				Role:      "user",
				Parts:     `[{"type":"text","data":{"text":"Fix the bug"}}]`,
				CreatedAt: 1700000000,
				UpdatedAt: 1700000000,
			},
			{
				ID:        "msg-2",
				SessionID: "ses-abc",
				Role:      "assistant",
				Parts:     `[{"type":"text","data":{"text":"Done."}}]`,
				Model:     "claude-opus-4",
				Provider:  "bedrock",
				CreatedAt: 1700000010,
				UpdatedAt: 1700000015,
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/sessions/ses-abc/export", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		json.NewEncoder(w).Encode(snapshot)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	result, err := client.Dump(context.Background(), "ses-abc")
	require.NoError(t, err)
	require.Equal(t, 1, result.Version)
	require.Equal(t, "ses-abc", result.Session.ID)
	require.Equal(t, "Fix auth bug", result.Session.Title)
	require.Len(t, result.Messages, 2)
	require.Equal(t, "user", result.Messages[0].Role)
	require.Equal(t, "assistant", result.Messages[1].Role)
	require.Equal(t, "claude-opus-4", result.Messages[1].Model)
}

func TestDumpNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ACPError{Code: 404, Message: `session "ses-xxx" not found`})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.Dump(context.Background(), "ses-xxx")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

// --- Restore ---

func TestRestore(t *testing.T) {
	t.Parallel()

	var received SessionSnapshot
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/sessions/import", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))

		json.NewEncoder(w).Encode(importResponse{
			SessionID:    received.Session.ID,
			MessageCount: len(received.Messages),
			Status:       "imported",
		})
	}))
	defer server.Close()

	snapshot := &SessionSnapshot{
		Version: 1,
		Session: SessionData{
			ID:           "ses-abc",
			Title:        "Fix auth bug",
			MessageCount: 2,
		},
		Messages: []SessionMessage{
			{ID: "msg-1", SessionID: "ses-abc", Role: "user", Parts: `[{"type":"text","data":{"text":"hello"}}]`},
			{ID: "msg-2", SessionID: "ses-abc", Role: "assistant", Parts: `[{"type":"text","data":{"text":"hi"}}]`},
		},
	}

	client := NewClient(server.URL)
	err := client.Restore(context.Background(), snapshot)
	require.NoError(t, err)

	require.Equal(t, 1, received.Version)
	require.Equal(t, "ses-abc", received.Session.ID)
	require.Len(t, received.Messages, 2)
}

func TestRestoreError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ACPError{Code: 400, Message: "snapshot version is required"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	err := client.Restore(context.Background(), &SessionSnapshot{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "snapshot version is required")
}

// --- Full round-trip: NewSession → Dump → Restore → Resume ---

func TestFullRoundTrip(t *testing.T) {
	t.Parallel()

	var savedSnapshot *SessionSnapshot
	var callCount atomic.Int32

	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/agents":
			json.NewEncoder(w).Encode(agentsListResponse{
				Agents: []AgentManifest{{Name: "crush"}},
			})

		case r.URL.Path == "/runs" && r.Method == http.MethodPost:
			count := callCount.Add(1)
			var req runCreateRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

			if count == 1 {
				json.NewEncoder(w).Encode(Run{
					AgentName: "crush",
					RunID:     "run-1",
					SessionID: "ses-roundtrip",
					Status:    RunStatusCompleted,
					Output:    []Message{NewAgentMessage("First response")},
					CreatedAt: time.Now(),
				})
			} else {
				require.Equal(t, "ses-roundtrip", req.SessionID)
				json.NewEncoder(w).Encode(Run{
					AgentName: "crush",
					RunID:     "run-2",
					SessionID: "ses-roundtrip",
					Status:    RunStatusCompleted,
					Output:    []Message{NewAgentMessage("Resumed response")},
					CreatedAt: time.Now(),
				})
			}

		case strings.HasPrefix(r.URL.Path, "/sessions/ses-roundtrip/export"):
			snap := SessionSnapshot{
				Version: 1,
				Session: SessionData{
					ID:           "ses-roundtrip",
					Title:        "Round trip test",
					MessageCount: 2,
				},
				Messages: []SessionMessage{
					{ID: "m1", SessionID: "ses-roundtrip", Role: "user", Parts: `[{"type":"text","data":{"text":"hello"}}]`},
					{ID: "m2", SessionID: "ses-roundtrip", Role: "assistant", Parts: `[{"type":"text","data":{"text":"First response"}}]`},
				},
			}
			json.NewEncoder(w).Encode(snap)

		case r.URL.Path == "/sessions/import":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&savedSnapshot))
			json.NewEncoder(w).Encode(importResponse{
				SessionID:    savedSnapshot.Session.ID,
				MessageCount: len(savedSnapshot.Messages),
				Status:       "imported",
			})
		}
	})
	defer server.Close()

	ctx := context.Background()
	client := NewClient(server.URL)

	// 1. Start new session.
	result, err := client.NewSession(ctx, "hello")
	require.NoError(t, err)
	require.Equal(t, "ses-roundtrip", result.Run.SessionID)
	require.Equal(t, "First response", result.Text())

	// 2. Dump.
	snapshot, err := client.Dump(ctx, result.Run.SessionID)
	require.NoError(t, err)
	require.Equal(t, "ses-roundtrip", snapshot.Session.ID)
	require.Len(t, snapshot.Messages, 2)

	// 3. Restore (simulating a new agent instance).
	err = client.Restore(ctx, snapshot)
	require.NoError(t, err)
	require.Equal(t, "ses-roundtrip", savedSnapshot.Session.ID)

	// 4. Resume.
	resumed, err := client.Resume(ctx, snapshot.Session.ID, "continue")
	require.NoError(t, err)
	require.Equal(t, "Resumed response", resumed.Text())
}

// --- WaitReady ---

func TestWaitReady(t *testing.T) {
	t.Parallel()

	var ready atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, "pong")
	}))
	defer server.Close()

	client := NewClient(server.URL)

	go func() {
		time.Sleep(150 * time.Millisecond)
		ready.Store(true)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.WaitReady(ctx, 50*time.Millisecond)
	require.NoError(t, err)
}

func TestWaitReadyTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := client.WaitReady(ctx, 50*time.Millisecond)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not ready")
}

// --- Options ---

func TestWithHeaders(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer my-token", r.Header.Get("Authorization"))
		fmt.Fprint(w, "pong")
	}))
	defer server.Close()

	client := NewClient(server.URL, WithHeaders(map[string]string{
		"Authorization": "Bearer my-token",
	}))
	require.NoError(t, client.Ping(context.Background()))
}

func TestWithAgentName(t *testing.T) {
	t.Parallel()

	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/runs" {
			var req runCreateRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			require.Equal(t, "my-agent", req.AgentName)

			json.NewEncoder(w).Encode(Run{
				AgentName: "my-agent",
				RunID:     "r1",
				Status:    RunStatusCompleted,
				Output:    []Message{NewAgentMessage("ok")},
				CreatedAt: time.Now(),
			})
		}
	})
	defer server.Close()

	client := NewClient(server.URL, WithAgentName("my-agent"))
	result, err := client.NewSession(context.Background(), "hi")
	require.NoError(t, err)
	require.Equal(t, "ok", result.Text())
}

func TestAutoDetectAgent(t *testing.T) {
	t.Parallel()

	var agentsQueried atomic.Bool
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agents" {
			agentsQueried.Store(true)
			json.NewEncoder(w).Encode(agentsListResponse{
				Agents: []AgentManifest{{Name: "auto-crush"}},
			})
			return
		}
		if r.URL.Path == "/runs" {
			var req runCreateRequest
			json.NewDecoder(r.Body).Decode(&req)
			require.Equal(t, "auto-crush", req.AgentName)

			json.NewEncoder(w).Encode(Run{
				AgentName: "auto-crush",
				RunID:     "r1",
				Status:    RunStatusCompleted,
				Output:    []Message{NewAgentMessage("ok")},
				CreatedAt: time.Now(),
			})
		}
	})
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.NewSession(context.Background(), "hi")
	require.NoError(t, err)
	require.True(t, agentsQueried.Load())
}

// --- Error handling ---

func TestServerError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ACPError{Code: 500, Message: "internal error"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.ListAgents(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "internal error")
}

func TestRunFailed(t *testing.T) {
	t.Parallel()

	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agents" {
			json.NewEncoder(w).Encode(agentsListResponse{
				Agents: []AgentManifest{{Name: "crush"}},
			})
			return
		}
		json.NewEncoder(w).Encode(Run{
			AgentName: "crush",
			RunID:     "r1",
			Status:    RunStatusFailed,
			Error:     &ACPError{Code: 500, Message: "agent crashed"},
			CreatedAt: time.Now(),
		})
	})
	defer server.Close()

	client := NewClient(server.URL)
	result, err := client.NewSession(context.Background(), "hi")
	require.NoError(t, err)
	require.Equal(t, RunStatusFailed, result.Run.Status)
	require.NotNil(t, result.Run.Error)
}

// --- Helpers ---

func TestTextContent(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{Role: "agent", Parts: []MessagePart{
			{ContentType: "text/plain", Content: "Hello"},
		}},
		{Role: "agent", Parts: []MessagePart{
			{ContentType: "text/plain", Content: "World"},
		}},
	}
	require.Equal(t, "Hello\nWorld", TextContent(messages))
}

func TestTextContentEmpty(t *testing.T) {
	t.Parallel()
	require.Equal(t, "", TextContent(nil))
}

func TestNewUserMessage(t *testing.T) {
	t.Parallel()

	msg := NewUserMessage("hello")
	require.Equal(t, "user", msg.Role)
	require.Len(t, msg.Parts, 1)
	require.Equal(t, "text/plain", msg.Parts[0].ContentType)
	require.Equal(t, "hello", msg.Parts[0].Content)
}

func TestNewAgentMessage(t *testing.T) {
	t.Parallel()

	msg := NewAgentMessage("hi")
	require.Equal(t, "agent", msg.Role)
	require.Len(t, msg.Parts, 1)
	require.Equal(t, "hi", msg.Parts[0].Content)
}

func TestRunStatusIsTerminal(t *testing.T) {
	t.Parallel()

	assert.True(t, RunStatusCompleted.IsTerminal())
	assert.True(t, RunStatusFailed.IsTerminal())
	assert.True(t, RunStatusCancelled.IsTerminal())
	assert.False(t, RunStatusCreated.IsTerminal())
	assert.False(t, RunStatusInProgress.IsTerminal())
	assert.False(t, RunStatusAwaiting.IsTerminal())
	assert.False(t, RunStatusCancelling.IsTerminal())
}

func TestSessionResultTextEmpty(t *testing.T) {
	t.Parallel()

	r := &SessionResult{}
	require.Equal(t, "", r.Text())
}

func TestSessionSnapshotInStream(t *testing.T) {
	t.Parallel()

	snapshot := SessionSnapshot{
		Version: 1,
		Session: SessionData{ID: "ses-snap", Title: "Snapshot test", MessageCount: 2},
		Messages: []SessionMessage{
			{ID: "m1", SessionID: "ses-snap", Role: "user", Parts: `[{"type":"text","data":{"text":"hi"}}]`},
		},
	}
	snapJSON, _ := json.Marshal(snapshot)

	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agents" {
			json.NewEncoder(w).Encode(agentsListResponse{
				Agents: []AgentManifest{{Name: "crush"}},
			})
			return
		}

		w.Header().Set("Content-Type", contentTypeNDJSON)
		w.WriteHeader(http.StatusOK)

		fmt.Fprintf(w, `{"type":"run.created","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-snap","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}`+"\n")
		fmt.Fprintf(w, `{"type":"session.snapshot","generic":%s}`+"\n", snapJSON)
		fmt.Fprintf(w, `{"type":"run.completed","run":{"agent_name":"crush","run_id":"r1","session_id":"ses-snap","status":"completed","output":[{"role":"agent","parts":[{"content_type":"text/plain","content":"ok"}]}],"created_at":"2025-01-01T00:00:00Z"}}`+"\n")
	})
	defer server.Close()

	client := NewClient(server.URL)
	stream, err := client.NewSessionStream(context.Background(), "hi")
	require.NoError(t, err)

	result, err := stream.Result()
	require.NoError(t, err)
	require.NotNil(t, result.Snapshot)
	require.Equal(t, "ses-snap", result.Snapshot.Session.ID)
	require.Len(t, result.Snapshot.Messages, 1)
}

// newMockServer creates a test server that handles requests.
func newMockServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}
