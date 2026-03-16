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
	// HookName is the name of the ACP server hook.
	HookName = "acp-server"

	// DefaultPort is the default HTTP server port.
	DefaultPort = 8199

	// DefaultAgentName is the ACP agent name for this Crush instance.
	DefaultAgentName = "crush"

	// RunTTL is how long completed runs are kept in memory.
	RunTTL = 1 * time.Hour

	// CleanupInterval is how often to clean up expired runs.
	CleanupInterval = 5 * time.Minute
)

// ServerConfig defines configuration for the ACP server hook.
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

// applyEnv overrides config fields with environment variables when set.
// Env vars take precedence over JSON config values.
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

	// activeRuns tracks run IDs to message event goroutines.
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
	}, nil
}

// Name returns the hook name.
func (h *ServerHook) Name() string {
	return HookName
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
	mux.HandleFunc("GET /ping", h.handlePing)

	addr := fmt.Sprintf(":%d", h.cfg.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	h.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // SSE needs no write timeout.
		IdleTimeout:  120 * time.Second,
	}

	// Periodic cleanup of expired runs.
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

	h.logger.Info("ACP server started", "addr", listener.Addr().String(), "agent", h.cfg.AgentName)

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

// Stop shuts down the ACP server.
func (h *ServerHook) Stop() error {
	if h.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return h.server.Shutdown(ctx)
	}
	return nil
}

// handlePing responds to health checks.
func (h *ServerHook) handlePing(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "pong")
}

// handleListAgents returns the single Crush agent manifest.
func (h *ServerHook) handleListAgents(w http.ResponseWriter, r *http.Request) {
	resp := AgentsListResponse{
		Agents: []AgentManifest{h.manifest},
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGetAgent returns the Crush agent manifest if name matches.
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
		h.handleStreamRun(w, r, rd, prompt, submitter)
	case RunModeAsync:
		go h.executeRun(r.Context(), rd, prompt, submitter)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(rd.getRun())
	default:
		h.executeRun(r.Context(), rd, prompt, submitter)
		writeJSON(w, http.StatusOK, rd.getRun())
	}
}

// handleStreamRun streams SSE events for a run.
func (h *ServerHook) handleStreamRun(w http.ResponseWriter, r *http.Request, rd *runData, prompt string, submitter plugin.PromptSubmitter) {
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

	// Send the initial run.created event that was already emitted.
	for _, e := range rd.getEvents() {
		writeSSE(w, e)
	}
	flusher.Flush()

	sub := rd.subscribe()

	go h.executeRun(r.Context(), rd, prompt, submitter)

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
func (h *ServerHook) executeRun(ctx context.Context, rd *runData, prompt string, submitter plugin.PromptSubmitter) {
	rd.setStatus(RunStatusInProgress)
	rd.emit(Event{Type: EventRunInProgress, Run: runPtr(rd.getRun())})

	// Subscribe to message events to capture the response.
	messages := h.app.Messages()
	var eventCh <-chan plugin.MessageEvent
	var cancelWatch context.CancelFunc

	if messages != nil {
		watchCtx, cancel := context.WithCancel(ctx)
		cancelWatch = cancel
		eventCh = messages.SubscribeMessages(watchCtx)
	}

	// Track assistant output from message events.
	var outputMu sync.Mutex
	var outputParts []string
	var lastContent string
	var wg sync.WaitGroup

	if eventCh != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for event := range eventCh {
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

	err := submitter.SubmitPrompt(ctx, prompt)

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

	rd.setOutput(output)
	rd.setStatus(RunStatusCompleted)
	rd.emit(Event{Type: EventRunCompleted, Run: runPtr(rd.getRun())})
}

// handleGetRun returns the current state of a run.
func (h *ServerHook) handleGetRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	rd := h.store.get(runID)
	if rd == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("run %q not found", runID))
		return
	}
	writeJSON(w, http.StatusOK, rd.getRun())
}

// handleListRunEvents returns all events for a run.
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

// handleCancelRun cancels an in-progress run.
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
