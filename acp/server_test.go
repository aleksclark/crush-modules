package acp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/plugin"
	"github.com/stretchr/testify/require"
)

func TestServerHandlePing(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()

	h.handlePing(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "pong", w.Body.String())
}

func TestServerHandleListAgents(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)
	req := httptest.NewRequest(http.MethodGet, "/agents", nil)
	w := httptest.NewRecorder()

	h.handleListAgents(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp AgentsListResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Agents, 1)
	require.Equal(t, "crush", resp.Agents[0].Name)
}

func TestServerHandleGetAgent(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /agents/{name}", h.handleGetAgent)

	req := httptest.NewRequest(http.MethodGet, "/agents/crush", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var manifest AgentManifest
	require.NoError(t, json.NewDecoder(w.Body).Decode(&manifest))
	require.Equal(t, "crush", manifest.Name)
}

func TestServerHandleGetAgentNotFound(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /agents/{name}", h.handleGetAgent)

	req := httptest.NewRequest(http.MethodGet, "/agents/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestServerHandleCreateRunSync(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)
	h.app = newMockApp(t, nil, nil)

	body := RunCreateRequest{
		AgentName: "crush",
		Input:     []Message{NewUserMessage("hello")},
		Mode:      RunModeSync,
	}
	data, _ := json.Marshal(body)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /runs", h.handleCreateRun)

	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(string(data)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var run Run
	require.NoError(t, json.NewDecoder(w.Body).Decode(&run))
	require.Equal(t, RunStatusCompleted, run.Status)
	require.NotEmpty(t, run.RunID)
}

func TestServerHandleCreateRunAsync(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)
	h.app = newMockApp(t, nil, nil)

	body := RunCreateRequest{
		AgentName: "crush",
		Input:     []Message{NewUserMessage("hello")},
		Mode:      RunModeAsync,
	}
	data, _ := json.Marshal(body)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /runs", h.handleCreateRun)

	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(string(data)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)

	var run Run
	require.NoError(t, json.NewDecoder(w.Body).Decode(&run))
	require.NotEmpty(t, run.RunID)
}

func TestServerHandleCreateRunBadAgent(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)

	body := RunCreateRequest{
		AgentName: "wrong-agent",
		Input:     []Message{NewUserMessage("hello")},
	}
	data, _ := json.Marshal(body)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /runs", h.handleCreateRun)

	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(string(data)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestServerHandleCreateRunNoInput(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)

	body := RunCreateRequest{
		AgentName: "crush",
		Input:     []Message{},
	}
	data, _ := json.Marshal(body)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /runs", h.handleCreateRun)

	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(string(data)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServerHandleGetRun(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)

	run := Run{RunID: "test-run-1", AgentName: "crush", Status: RunStatusCompleted, Output: []Message{NewAgentMessage("done")}, CreatedAt: time.Now()}
	h.store.create(run)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /runs/{run_id}", h.handleGetRun)

	req := httptest.NewRequest(http.MethodGet, "/runs/test-run-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var got Run
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Equal(t, "test-run-1", got.RunID)
	require.Equal(t, RunStatusCompleted, got.Status)
}

func TestServerHandleGetRunNotFound(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /runs/{run_id}", h.handleGetRun)

	req := httptest.NewRequest(http.MethodGet, "/runs/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestServerHandleCancelRun(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)

	run := Run{RunID: "cancel-run", AgentName: "crush", Status: RunStatusInProgress, Output: []Message{}, CreatedAt: time.Now()}
	h.store.create(run)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /runs/{run_id}/cancel", h.handleCancelRun)

	req := httptest.NewRequest(http.MethodPost, "/runs/cancel-run/cancel", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)

	var got Run
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Equal(t, RunStatusCancelled, got.Status)
}

func TestServerHandleCancelRunAlreadyDone(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)

	run := Run{RunID: "done-run", AgentName: "crush", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now()}
	rd := h.store.create(run)
	rd.setStatus(RunStatusCompleted)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /runs/{run_id}/cancel", h.handleCancelRun)

	req := httptest.NewRequest(http.MethodPost, "/runs/done-run/cancel", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code)
}

func TestServerHandleListRunEvents(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)

	run := Run{RunID: "event-run", AgentName: "crush", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now()}
	rd := h.store.create(run)
	rd.emit(Event{Type: EventRunCreated})
	rd.emit(Event{Type: EventRunInProgress})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /runs/{run_id}/events", h.handleListRunEvents)

	req := httptest.NewRequest(http.MethodGet, "/runs/event-run/events", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var result struct {
		Events []Event `json:"events"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	require.Len(t, result.Events, 2)
}

func TestServerExecuteRun(t *testing.T) {
	t.Parallel()

	h := newTestServerHook(t)
	h.app = newMockApp(t, nil, nil)

	run := Run{RunID: "exec-run", AgentName: "crush", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now()}
	rd := h.store.create(run)

	submitter := h.app.PromptSubmitter()

	ctx := context.Background()
	h.executeRun(ctx, rd, "hello world", "", submitter)

	got := rd.getRun()
	require.Equal(t, RunStatusCompleted, got.Status)
	require.NotNil(t, got.FinishedAt)
}

func TestServerExecuteRunWithMessages(t *testing.T) {
	t.Parallel()

	msgSub := &mockMessageSubscriber{
		events: []plugin.MessageEvent{
			{
				Type: plugin.MessageCreated,
				Message: plugin.Message{
					Role:    plugin.MessageRoleAssistant,
					Content: "Hello",
				},
			},
			{
				Type: plugin.MessageUpdated,
				Message: plugin.Message{
					Role:    plugin.MessageRoleAssistant,
					Content: "Hello World",
				},
			},
		},
	}

	h := newTestServerHook(t)
	h.app = newMockApp(t, nil, msgSub)

	run := Run{RunID: "msg-run", AgentName: "crush", Status: RunStatusCreated, Output: []Message{}, CreatedAt: time.Now()}
	rd := h.store.create(run)

	ctx := context.Background()
	h.executeRun(ctx, rd, "test", "", h.app.PromptSubmitter())

	got := rd.getRun()
	require.Equal(t, RunStatusCompleted, got.Status)
	require.Len(t, got.Output, 1)
	require.Equal(t, "Hello World", got.Output[0].Parts[0].Content)
}

func TestNewServerHookDefaults(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp(plugin.WithLogger(nil))
	hook, err := NewServerHook(app, ACPServerConfig{})
	require.NoError(t, err)
	require.NotNil(t, hook)
	require.Equal(t, DefaultPort, hook.cfg.Port)
	require.Equal(t, DefaultAgentName, hook.cfg.AgentName)
	require.Equal(t, HookName, hook.Name())
}

// Test helpers.

func newTestServerHook(t *testing.T) *ServerHook {
	t.Helper()
	app := plugin.NewApp(plugin.WithLogger(nil))
	hook, err := NewServerHook(app, ACPServerConfig{})
	require.NoError(t, err)
	return hook
}

func newMockApp(t *testing.T, promptErr error, msgSub plugin.MessageSubscriber) *plugin.App {
	t.Helper()
	ps := &mockPromptSubmitter{err: promptErr}
	if ms, ok := msgSub.(*mockMessageSubscriber); ok && ms != nil {
		ms.sent = make(chan struct{})
		ps.waitFor = ms.sent
	}
	opts := []plugin.AppOption{
		plugin.WithLogger(nil),
		plugin.WithPromptSubmitter(ps),
	}
	if msgSub != nil {
		opts = append(opts, plugin.WithMessageSubscriber(msgSub))
	}
	return plugin.NewApp(opts...)
}

type mockPromptSubmitter struct {
	err     error
	waitFor chan struct{}
}

func (m *mockPromptSubmitter) SubmitPrompt(_ context.Context, _ string) error {
	if m.waitFor != nil {
		<-m.waitFor
	}
	return m.err
}

func (m *mockPromptSubmitter) SubmitPromptToSession(_ context.Context, _, _ string) error {
	if m.waitFor != nil {
		<-m.waitFor
	}
	return m.err
}

func (m *mockPromptSubmitter) CurrentSessionID() string {
	return "test-session"
}

func (m *mockPromptSubmitter) IsSessionBusy() bool {
	return false
}

type mockMessageSubscriber struct {
	events []plugin.MessageEvent
	sent   chan struct{}
}

func (m *mockMessageSubscriber) SubscribeMessages(ctx context.Context) <-chan plugin.MessageEvent {
	ch := make(chan plugin.MessageEvent, len(m.events))
	go func() {
		defer close(ch)
		for _, e := range m.events {
			ch <- e
		}
		if m.sent != nil {
			close(m.sent)
		}
		<-ctx.Done()
	}()
	return ch
}

func TestACPServerConfigApplyEnv(t *testing.T) {
	t.Run("overrides all fields", func(t *testing.T) {
		t.Setenv("CRUSH_ACP_PORT", "9999")
		t.Setenv("CRUSH_ACP_AGENT_NAME", "my-agent")
		t.Setenv("CRUSH_ACP_DESCRIPTION", "custom desc")

		cfg := ACPServerConfig{Port: 8199, AgentName: "crush", Description: "default"}
		cfg.applyEnv()

		require.Equal(t, 9999, cfg.Port)
		require.Equal(t, "my-agent", cfg.AgentName)
		require.Equal(t, "custom desc", cfg.Description)
	})

	t.Run("leaves unset fields unchanged", func(t *testing.T) {
		cfg := ACPServerConfig{Port: 8199, AgentName: "crush", Description: "default"}
		cfg.applyEnv()

		require.Equal(t, 8199, cfg.Port)
		require.Equal(t, "crush", cfg.AgentName)
		require.Equal(t, "default", cfg.Description)
	})

	t.Run("ignores invalid port", func(t *testing.T) {
		t.Setenv("CRUSH_ACP_PORT", "notanumber")

		cfg := ACPServerConfig{Port: 8199}
		cfg.applyEnv()

		require.Equal(t, 8199, cfg.Port)
	})

	t.Run("ignores zero port", func(t *testing.T) {
		t.Setenv("CRUSH_ACP_PORT", "0")

		cfg := ACPServerConfig{Port: 8199}
		cfg.applyEnv()

		require.Equal(t, 8199, cfg.Port)
	})

	t.Run("env overrides json config", func(t *testing.T) {
		t.Setenv("CRUSH_ACP_PORT", "4000")

		cfg := ACPServerConfig{Port: 3000, AgentName: "from-json"}
		cfg.applyEnv()

		require.Equal(t, 4000, cfg.Port)
		require.Equal(t, "from-json", cfg.AgentName)
	})
}
