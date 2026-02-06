// Package tempotown provides integration with the Tempotown orchestrator.
//
// This plugin allows Crush to act as an agent within the Tempotown ensemble,
// reporting status and receiving signals from Temporal workflows.
//
// The plugin is DISABLED by default. To enable it, you must explicitly
// configure an endpoint in crush.json:
//
//	{
//	  "options": {
//	    "plugins": {
//	      "tempotown": {
//	        "endpoint": "localhost:9090",
//	        "role": "coder",
//	        "capabilities": ["code", "test"]
//	      }
//	    }
//	  }
//	}
//
// Without an endpoint configured, the plugin does nothing.
package tempotown

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/crush/plugin"
)

const (
	// HookName is the name of the Tempotown hook.
	HookName = "tempotown"

	// DefaultRole is the default agent role.
	DefaultRole = "coder"

	// DefaultPollInterval is how often to poll for signals.
	DefaultPollInterval = 5 * time.Second

	// ReconnectDelay is how long to wait before reconnecting.
	ReconnectDelay = 5 * time.Second
)

// Config defines the configuration options for the Tempotown plugin.
type Config struct {
	// Endpoint is the MCP server address (e.g., "localhost:9090").
	// REQUIRED: If empty, the plugin is disabled and does not connect.
	Endpoint string `json:"endpoint,omitempty"`

	// Role is the agent role: coder, reviewer, merger, supervisor.
	Role string `json:"role,omitempty"`

	// Capabilities is a list of agent capabilities.
	Capabilities []string `json:"capabilities,omitempty"`

	// PollInterval is how often to poll for signals (default: 5s).
	PollIntervalSeconds int `json:"poll_interval_seconds,omitempty"`
}

func init() {
	plugin.RegisterHookWithConfig(HookName, func(ctx context.Context, app *plugin.App) (plugin.Hook, error) {
		var cfg Config
		if err := app.LoadConfig(HookName, &cfg); err != nil {
			return nil, err
		}
		hook, err := NewTempotownHook(app, cfg)
		if err != nil {
			return nil, err
		}
		if hook == nil {
			// No endpoint configured - hook is disabled
			return nil, nil
		}
		return hook, nil
	}, &Config{})
}

// TempotownHook implements the plugin.Hook interface for Tempotown integration.
type TempotownHook struct {
	app    *plugin.App
	cfg    Config
	logger *slog.Logger

	// MCP client state.
	mu        sync.Mutex
	conn      net.Conn
	encoder   *json.Encoder
	decoder   *json.Decoder
	requestID atomic.Int64
	pending   map[int64]chan *Response

	// Agent state.
	agentID     string
	currentTask string
	phase       string
	connected   atomic.Bool

	// Feedback channel for injecting signals into Crush.
	feedbackCh chan FeedbackPayload
}

// NewTempotownHook creates a new Tempotown hook.
func NewTempotownHook(app *plugin.App, cfg Config) (*TempotownHook, error) {
	// Endpoint is required - if not configured, the hook is disabled
	if cfg.Endpoint == "" {
		return nil, nil // Return nil hook to indicate disabled
	}
	if cfg.Role == "" {
		cfg.Role = DefaultRole
	}
	if cfg.PollIntervalSeconds == 0 {
		cfg.PollIntervalSeconds = int(DefaultPollInterval / time.Second)
	}

	var logger *slog.Logger
	if app != nil {
		logger = app.Logger().With("hook", HookName)
	} else {
		logger = slog.Default().With("hook", HookName)
	}

	hook := &TempotownHook{
		app:        app,
		cfg:        cfg,
		logger:     logger,
		pending:    make(map[int64]chan *Response),
		feedbackCh: make(chan FeedbackPayload, 10),
		phase:      "init",
	}

	return hook, nil
}

// Name returns the hook identifier.
func (h *TempotownHook) Name() string {
	return HookName
}

// Start begins the Tempotown integration.
func (h *TempotownHook) Start(ctx context.Context) error {
	// Start connection manager in background.
	go h.connectionLoop(ctx)

	// Start feedback poll loop.
	go h.pollFeedbackLoop(ctx)

	// Start message event handler.
	messages := h.app.Messages()
	if messages == nil {
		h.logger.Warn("no message subscriber available, status reporting disabled")
		<-ctx.Done()
		return h.Stop()
	}

	events := messages.SubscribeMessages(ctx)
	h.logger.Info("Tempotown hook started", "endpoint", h.cfg.Endpoint, "role", h.cfg.Role)

	for {
		select {
		case <-ctx.Done():
			return h.Stop()
		case event, ok := <-events:
			if !ok {
				return h.Stop()
			}
			h.handleEvent(ctx, event)
		}
	}
}

// Stop gracefully shuts down the hook.
func (h *TempotownHook) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.conn != nil {
		h.conn.Close()
		h.conn = nil
	}
	h.connected.Store(false)
	h.logger.Info("Tempotown hook stopped")
	return nil
}

// connectionLoop manages the connection to the MCP server.
func (h *TempotownHook) connectionLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		done, err := h.connect(ctx)
		if err != nil {
			h.logger.Warn("failed to connect to Tempotown", "error", err, "endpoint", h.cfg.Endpoint)
			select {
			case <-ctx.Done():
				return
			case <-time.After(ReconnectDelay):
				continue
			}
		}

		// Wait for connection to drop.
		select {
		case <-ctx.Done():
			return
		case <-done:
		}

		// Connection lost, try to reconnect.
		h.connected.Store(false)
		h.logger.Info("connection lost, reconnecting...")
		select {
		case <-ctx.Done():
			return
		case <-time.After(ReconnectDelay):
		}
	}
}

// connect establishes connection to the MCP server.
// Returns a channel that closes when the connection is lost.
func (h *TempotownHook) connect(ctx context.Context) (<-chan struct{}, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", h.cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("dial failed: %w", err)
	}

	h.mu.Lock()
	h.conn = conn
	h.encoder = json.NewEncoder(conn)
	h.decoder = json.NewDecoder(bufio.NewReader(conn))
	h.mu.Unlock()

	// Start reading responses in background.
	// This is needed because initialize() and registerAgent() make calls
	// that expect responses.
	done := make(chan struct{})
	go func() {
		h.readLoop(ctx)
		close(done)
	}()

	// Initialize MCP protocol.
	if err := h.initialize(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("initialize failed: %w", err)
	}

	// Register as agent.
	if err := h.registerAgent(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("register failed: %w", err)
	}

	h.connected.Store(true)
	h.logger.Info("connected to Tempotown", "agent_id", h.agentID)
	return done, nil
}

// readLoop reads responses from the server.
func (h *TempotownHook) readLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var resp Response
		if err := h.decoder.Decode(&resp); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			h.logger.Error("read error", "error", err)
			return
		}

		// Route response to waiting caller.
		if resp.ID != nil {
			if id, ok := resp.ID.(float64); ok {
				h.mu.Lock()
				if ch, exists := h.pending[int64(id)]; exists {
					ch <- &resp
					delete(h.pending, int64(id))
				}
				h.mu.Unlock()
			}
		}
	}
}

// initialize performs MCP protocol initialization.
func (h *TempotownHook) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProtocolVersion: "2024-11-05",
		ClientInfo: Implementation{
			Name:    "crush",
			Version: "1.0.0",
		},
		Capabilities: ClientCapability{},
	}

	_, err := h.call(ctx, "initialize", params)
	if err != nil {
		return err
	}

	// Send initialized notification.
	h.sendNotification("initialized", nil)
	return nil
}

// registerAgent registers this Crush instance with Tempotown.
func (h *TempotownHook) registerAgent(ctx context.Context) error {
	args := map[string]any{
		"role":         h.cfg.Role,
		"capabilities": h.cfg.Capabilities,
	}

	resp, err := h.callTool(ctx, "register_agent", args)
	if err != nil {
		return err
	}

	// Parse agent ID from response.
	var result struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(resp), &result); err == nil && result.AgentID != "" {
		h.agentID = result.AgentID
	}

	h.phase = "idle"
	return nil
}

// call makes a JSON-RPC call and waits for response.
func (h *TempotownHook) call(ctx context.Context, method string, params any) (*Response, error) {
	id := h.requestID.Add(1)
	ch := make(chan *Response, 1)

	h.mu.Lock()
	h.pending[id] = ch
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
	}
	if params != nil {
		data, _ := json.Marshal(params)
		req.Params = data
	}
	err := h.encoder.Encode(req)
	h.mu.Unlock()

	if err != nil {
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp, nil
	case <-time.After(30 * time.Second):
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		return nil, fmt.Errorf("request timeout")
	}
}

// callTool invokes an MCP tool and returns the text result.
func (h *TempotownHook) callTool(ctx context.Context, name string, args map[string]any) (string, error) {
	argsJSON, _ := json.Marshal(args)
	params := ToolCallParams{
		Name:      name,
		Arguments: argsJSON,
	}

	resp, err := h.call(ctx, "tools/call", params)
	if err != nil {
		return "", err
	}

	var result ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("unmarshal result: %w", err)
	}

	if result.IsError {
		if len(result.Content) > 0 {
			return "", fmt.Errorf("tool error: %s", result.Content[0].Text)
		}
		return "", fmt.Errorf("tool error")
	}

	if len(result.Content) > 0 {
		return result.Content[0].Text, nil
	}
	return "", nil
}

// sendNotification sends a JSON-RPC notification (no response expected).
func (h *TempotownHook) sendNotification(method string, params any) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.encoder == nil {
		return
	}

	notif := Notification{
		JSONRPC: "2.0",
		Method:  method,
	}
	if params != nil {
		data, _ := json.Marshal(params)
		notif.Params = data
	}
	_ = h.encoder.Encode(notif)
}

// handleEvent processes message events and reports status.
func (h *TempotownHook) handleEvent(ctx context.Context, event plugin.MessageEvent) {
	if !h.connected.Load() {
		return
	}

	msg := event.Message

	switch event.Type {
	case plugin.MessageCreated:
		switch msg.Role {
		case plugin.MessageRoleUser:
			h.reportStatus(ctx, "processing user input", 0, nil)
		case plugin.MessageRoleAssistant:
			h.reportStatus(ctx, "generating response", 50, nil)
		}

	case plugin.MessageUpdated:
		if msg.Role == plugin.MessageRoleAssistant {
			// Check for active tool calls.
			for _, tc := range msg.ToolCalls {
				if !tc.Finished {
					h.reportStatus(ctx, fmt.Sprintf("running tool: %s", tc.Name), 50, map[string]any{
						"tool":    tc.Name,
						"tool_id": tc.ID,
					})
					return
				}
			}
			h.reportStatus(ctx, "response complete", 100, nil)
		}
	}
}

// reportStatus sends a status update to Tempotown.
func (h *TempotownHook) reportStatus(ctx context.Context, status string, progress int, details map[string]any) {
	if !h.connected.Load() {
		return
	}

	args := map[string]any{
		"status":   status,
		"progress": progress,
	}
	if details != nil {
		args["details"] = details
	}

	go func() {
		if _, err := h.callTool(ctx, "report_status", args); err != nil {
			h.logger.Debug("failed to report status", "error", err)
		}
	}()
}

// pollFeedbackLoop periodically polls for feedback/signals.
func (h *TempotownHook) pollFeedbackLoop(ctx context.Context) {
	interval := time.Duration(h.cfg.PollIntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if h.connected.Load() {
				h.pollFeedback(ctx)
			}
		}
	}
}

// pollFeedback checks for pending feedback/signals.
func (h *TempotownHook) pollFeedback(ctx context.Context) {
	result, err := h.callTool(ctx, "get_pending_feedback", map[string]any{"limit": 10})
	if err != nil {
		h.logger.Debug("failed to poll feedback", "error", err)
		return
	}

	var feedback struct {
		Items []FeedbackPayload `json:"items"`
	}
	if err := json.Unmarshal([]byte(result), &feedback); err != nil {
		return
	}

	for _, item := range feedback.Items {
		select {
		case h.feedbackCh <- item:
		default:
			// Channel full, drop feedback.
			h.logger.Warn("feedback channel full, dropping", "source", item.Source)
		}
	}
}

// FeedbackCh returns the channel for receiving feedback from Tempotown.
// External components can listen to this to inject signals into the agent.
func (h *TempotownHook) FeedbackCh() <-chan FeedbackPayload {
	return h.feedbackCh
}

// IsConnected returns whether the hook is connected to Tempotown.
func (h *TempotownHook) IsConnected() bool {
	return h.connected.Load()
}

// MCP Protocol Types (subset needed for client).

// Request is a JSON-RPC request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Notification is a JSON-RPC notification.
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Error is a JSON-RPC error.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// InitializeParams is the params for the initialize request.
type InitializeParams struct {
	ProtocolVersion string           `json:"protocolVersion"`
	ClientInfo      Implementation   `json:"clientInfo"`
	Capabilities    ClientCapability `json:"capabilities"`
}

// Implementation describes a client or server.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCapability describes client capabilities.
type ClientCapability struct {
	Roots    *RootsCapability    `json:"roots,omitempty"`
	Sampling *SamplingCapability `json:"sampling,omitempty"`
}

// RootsCapability describes root capabilities.
type RootsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// SamplingCapability describes sampling capabilities.
type SamplingCapability struct{}

// ToolCallParams is the params for tools/call.
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolCallResult is the result of tools/call.
type ToolCallResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Content is a content block in a tool result.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// FeedbackPayload is feedback from Tempotown.
type FeedbackPayload struct {
	Message  string         `json:"message"`
	Source   string         `json:"source"`
	TaskID   string         `json:"task_id,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}
