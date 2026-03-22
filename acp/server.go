package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/crush/plugin"
	"github.com/google/uuid"
)

const (
	HookName         = "acp-server"
	DefaultPort      = 8199
	DefaultAgentName = "crush"
	RunTTL           = 1 * time.Hour
	CleanupInterval  = 5 * time.Minute
)

// ACPServerConfig defines configuration for the ACP server hook.
type ACPServerConfig struct {
	Port        int    `json:"port,omitempty"`
	AgentName   string `json:"agent_name,omitempty"`
	Description string `json:"description,omitempty"`
}

func init() {
	plugin.RegisterHookWithConfig(HookName, func(ctx context.Context, app *plugin.App) (plugin.Hook, error) {
		var cfg ACPServerConfig
		if err := app.LoadConfig(HookName, &cfg); err != nil {
			return nil, err
		}
		cfg.applyEnv()
		return NewServerHook(app, cfg)
	}, &ACPServerConfig{})
}

func (c *ACPServerConfig) applyEnv() {
	if v := os.Getenv("CRUSH_ACP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			c.Port = p
		}
	}
	if v := os.Getenv("CRUSH_ACP_AGENT_NAME"); v != "" {
		c.AgentName = v
	}
	if v := os.Getenv("CRUSH_ACP_DESCRIPTION"); v != "" {
		c.Description = v
	}
}

// ServerHook implements plugin.Hook to expose Crush as an ACP server.
type ServerHook struct {
	app    *plugin.App
	cfg    ACPServerConfig
	logger *slog.Logger

	store    *runStore
	manifest AgentManifest
	server   *http.Server
	addr     string
	ready    chan struct{}
	ctx      context.Context

	activeRuns sync.Map
}

// NewServerHook creates a new ACP server hook.
func NewServerHook(app *plugin.App, cfg ACPServerConfig) (*ServerHook, error) {
	if cfg.Port <= 0 {
		cfg.Port = DefaultPort
	}
	if cfg.AgentName == "" {
		cfg.AgentName = DefaultAgentName
	}
	if cfg.Description == "" {
		cfg.Description = "Crush AI coding assistant exposed as an ACP agent"
	}

	logger := app.Logger().With("hook", HookName)

	manifest := AgentManifest{
		Name:               cfg.AgentName,
		Description:        cfg.Description,
		InputContentTypes:  []string{"text/plain"},
		OutputContentTypes: []string{"text/plain"},
		Metadata: &AgentMetadata{
			Framework:        "Crush",
			NaturalLanguages: []string{"en"},
			Capabilities: []AgentCapability{
				{Name: "code", Description: "Write, review, and debug code"},
				{Name: "tools", Description: "Execute tools like file editing, search, and shell commands"},
				{Name: "sessions", Description: "Persistent sessions with export/import for crash recovery"},
			},
			Tags: []string{"coding", "AI assistant"},
		},
	}

	return &ServerHook{
		app:      app,
		cfg:      cfg,
		logger:   logger,
		store:    newRunStore(),
		manifest: manifest,
		ready:    make(chan struct{}),
	}, nil
}

func (h *ServerHook) Name() string {
	return HookName
}

// Addr returns the address the server is listening on.
// Only valid after Ready() returns.
func (h *ServerHook) Addr() string {
	return h.addr
}

// Ready returns a channel that is closed when the server is listening.
func (h *ServerHook) Ready() <-chan struct{} {
	return h.ready
}

// Start begins the ACP server.
func (h *ServerHook) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agents", h.handleListAgents)
	mux.HandleFunc("GET /agents/{name}", h.handleGetAgent)
	mux.HandleFunc("POST /runs", h.handleCreateRun)
	mux.HandleFunc("GET /runs/{run_id}", h.handleGetRun)
	mux.HandleFunc("GET /runs/{run_id}/events", h.handleListRunEvents)
	mux.HandleFunc("POST /runs/{run_id}/cancel", h.handleCancelRun)
	mux.HandleFunc("GET /sessions/{session_id}/export", h.handleExportSession)
	mux.HandleFunc("POST /sessions/import", h.handleImportSession)
	mux.HandleFunc("GET /ping", h.handlePing)

	addr := fmt.Sprintf(":%d", h.cfg.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	h.addr = listener.Addr().String()
	h.server = &http.Server{
		Addr:         h.addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		ticker := time.NewTicker(CleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.store.cleanup(RunTTL)
			}
		}
	}()

	h.ctx = ctx
	close(h.ready)
	h.logger.Info("ACP server started", "addr", h.addr, "agent", h.cfg.AgentName)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.server.Shutdown(shutdownCtx)
	}()

	if err := h.server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (h *ServerHook) Stop() error {
	if h.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return h.server.Shutdown(ctx)
	}
	return nil
}

func (h *ServerHook) handlePing(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "pong")
}

func (h *ServerHook) handleListAgents(w http.ResponseWriter, r *http.Request) {
	resp := AgentsListResponse{
		Agents: []AgentManifest{h.manifest},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *ServerHook) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name != h.manifest.Name {
		writeError(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", name))
		return
	}
	writeJSON(w, http.StatusOK, h.manifest)
}

// handleCreateRun creates a new run by submitting a prompt to Crush.
func (h *ServerHook) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var req RunCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	if req.AgentName != h.manifest.Name {
		writeError(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", req.AgentName))
		return
	}

	if len(req.Input) == 0 {
		writeError(w, http.StatusBadRequest, "input is required")
		return
	}

	prompt := TextContent(req.Input)
	if prompt == "" {
		writeError(w, http.StatusBadRequest, "input must contain text content")
		return
	}

	submitter := h.app.PromptSubmitter()
	if submitter == nil {
		writeError(w, http.StatusServiceUnavailable, "prompt submitter not available")
		return
	}

	runID := uuid.New().String()
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = submitter.CurrentSessionID()
	}

	now := time.Now()
	run := Run{
		AgentName: h.manifest.Name,
		RunID:     runID,
		SessionID: sessionID,
		Status:    RunStatusCreated,
		Output:    []Message{},
		CreatedAt: now,
	}
	rd := h.store.create(run)

	rd.emit(Event{Type: EventRunCreated, Run: &run})

	mode := req.Mode
	if mode == "" {
		mode = RunModeSync
	}

	switch mode {
	case RunModeStream:
		h.handleStreamRun(w, r, rd, prompt, sessionID, submitter)
	case RunModeAsync:
		go h.executeRun(h.ctx, rd, prompt, sessionID, submitter)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(rd.getRun())
	default:
		h.executeRun(r.Context(), rd, prompt, sessionID, submitter)
		writeJSON(w, http.StatusOK, rd.getRun())
	}
}

// handleStreamRun streams SSE events for a run, including session message updates.
func (h *ServerHook) handleStreamRun(w http.ResponseWriter, r *http.Request, rd *runData, prompt, sessionID string, submitter plugin.PromptSubmitter) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Run-ID", rd.getRun().RunID)
	w.WriteHeader(http.StatusOK)

	for _, e := range rd.getEvents() {
		writeSSE(w, e)
	}
	flusher.Flush()

	sub := rd.subscribe()

	go h.executeRun(r.Context(), rd, prompt, sessionID, submitter)

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-sub:
			if !ok {
				return
			}
			writeSSE(w, event)
			flusher.Flush()
		}
	}
}

// executeRun runs a prompt through Crush and tracks the run lifecycle.
// It streams both ACP-level events and raw session message updates for crash recovery.
func (h *ServerHook) executeRun(ctx context.Context, rd *runData, prompt, sessionID string, submitter plugin.PromptSubmitter) {
	rd.setStatus(RunStatusInProgress)
	rd.emit(Event{Type: EventRunInProgress, Run: runPtr(rd.getRun())})

	messages := h.app.Messages()
	var eventCh <-chan plugin.MessageEvent
	var cancelWatch context.CancelFunc

	if messages != nil {
		watchCtx, cancel := context.WithCancel(ctx)
		cancelWatch = cancel
		eventCh = messages.SubscribeMessages(watchCtx)
	}

	var outputMu sync.Mutex
	var outputParts []string
	var lastContent string
	var wg sync.WaitGroup

	if eventCh != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for event := range eventCh {
				// Emit raw session message updates for crash recovery.
				// The client can use these to reconstruct the full session state.
				h.emitSessionMessage(rd, event)

				if event.Message.Role != plugin.MessageRoleAssistant {
					continue
				}
				content := event.Message.Content
				outputMu.Lock()
				if content != lastContent && content != "" {
					newContent := content
					if strings.HasPrefix(content, lastContent) {
						newContent = content[len(lastContent):]
					}
					if newContent != "" {
						outputParts = append(outputParts, newContent)
						rd.emit(Event{
							Type: EventMessagePart,
							Part: &MessagePart{
								ContentType: "text/plain",
								Content:     newContent,
							},
						})
					}
					lastContent = content
				}
				outputMu.Unlock()
			}
		}()
	}

	var err error
	if sessionID != "" {
		err = submitter.SubmitPromptToSession(ctx, sessionID, prompt)
	} else {
		err = submitter.SubmitPrompt(ctx, prompt)
	}

	if cancelWatch != nil {
		cancelWatch()
	}
	wg.Wait()

	outputMu.Lock()
	finalContent := lastContent
	outputMu.Unlock()

	if err != nil {
		acpErr := &ACPError{Message: err.Error()}
		rd.setError(acpErr)
		rd.setStatus(RunStatusFailed)
		rd.emit(Event{Type: EventRunFailed, Run: runPtr(rd.getRun())})
		return
	}

	output := []Message{}
	if finalContent != "" {
		msg := NewAgentMessage(finalContent)
		now := time.Now()
		msg.CompletedAt = &now
		output = append(output, msg)
		rd.emit(Event{Type: EventMessageCompleted, Message: &msg})
	}

	// Emit a final session snapshot event so the client has the complete state.
	h.emitSessionSnapshot(rd)

	rd.setOutput(output)
	rd.setStatus(RunStatusCompleted)
	rd.emit(Event{Type: EventRunCompleted, Run: runPtr(rd.getRun())})
}

// emitSessionMessage emits a session.message event with the raw message data.
func (h *ServerHook) emitSessionMessage(rd *runData, event plugin.MessageEvent) {
	msg := event.Message
	sm := SessionMessageEvent{
		EventType: string(event.Type),
		MessageID: msg.ID,
		SessionID: msg.SessionID,
		Role:      string(msg.Role),
		Content:   msg.Content,
	}

	for _, tc := range msg.ToolCalls {
		sm.ToolCalls = append(sm.ToolCalls, SessionToolCall{
			ID:       tc.ID,
			Name:     tc.Name,
			Input:    tc.Input,
			Finished: tc.Finished,
		})
	}

	for _, tr := range msg.ToolResults {
		sm.ToolResults = append(sm.ToolResults, SessionToolResult{
			ToolCallID: tr.ToolCallID,
			Name:       tr.Name,
			Content:    tr.Content,
			IsError:    tr.IsError,
		})
	}

	rd.emit(Event{
		Type:    EventSessionMessage,
		Generic: sm,
	})
}

// emitSessionSnapshot emits a session export event so the client can persist the full state.
func (h *ServerHook) emitSessionSnapshot(rd *runData) {
	store := h.app.SessionStore()
	if store == nil {
		return
	}

	run := rd.getRun()
	if run.SessionID == "" {
		return
	}

	snapshot, err := store.ExportSession(context.Background(), run.SessionID)
	if err != nil {
		h.logger.Warn("failed to export session for snapshot event", "session_id", run.SessionID, "error", err)
		return
	}

	rd.emit(Event{
		Type:    EventSessionSnapshot,
		Generic: snapshot,
	})
}

// handleExportSession exports a full session snapshot.
func (h *ServerHook) handleExportSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")

	store := h.app.SessionStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "session store not available")
		return
	}

	snapshot, err := store.ExportSession(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("session %q not found: %v", sessionID, err))
		return
	}

	writeJSON(w, http.StatusOK, snapshot)
}

// handleImportSession imports a session snapshot.
func (h *ServerHook) handleImportSession(w http.ResponseWriter, r *http.Request) {
	store := h.app.SessionStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "session store not available")
		return
	}

	var snapshot plugin.SessionSnapshot
	if err := json.NewDecoder(r.Body).Decode(&snapshot); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid snapshot: %v", err))
		return
	}

	if snapshot.Version == 0 {
		writeError(w, http.StatusBadRequest, "snapshot version is required")
		return
	}

	if snapshot.Session.ID == "" {
		writeError(w, http.StatusBadRequest, "session ID is required")
		return
	}

	if err := store.ImportSession(r.Context(), snapshot); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("import failed: %v", err))
		return
	}

	h.logger.Info("session imported", "session_id", snapshot.Session.ID, "messages", len(snapshot.Messages))
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":    snapshot.Session.ID,
		"message_count": len(snapshot.Messages),
		"status":        "imported",
	})
}

func (h *ServerHook) handleGetRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	rd := h.store.get(runID)
	if rd == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("run %q not found", runID))
		return
	}
	writeJSON(w, http.StatusOK, rd.getRun())
}

func (h *ServerHook) handleListRunEvents(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	rd := h.store.get(runID)
	if rd == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("run %q not found", runID))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events": rd.getEvents(),
	})
}

func (h *ServerHook) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	rd := h.store.get(runID)
	if rd == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("run %q not found", runID))
		return
	}

	run := rd.getRun()
	if run.Status.IsTerminal() {
		writeError(w, http.StatusConflict, fmt.Sprintf("run is already in terminal state: %s", run.Status))
		return
	}

	rd.setStatus(RunStatusCancelled)
	rd.emit(Event{Type: EventRunCancelled, Run: runPtr(rd.getRun())})
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(rd.getRun())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ACPError{Code: status, Message: message})
}

func writeSSE(w http.ResponseWriter, event Event) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func runPtr(r Run) *Run {
	return &r
}
