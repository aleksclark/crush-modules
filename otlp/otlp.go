// Package otlp provides an OTLP tracing plugin for Crush.
//
// The plugin exports traces for chat messages and tool calls to an OTLP-compatible
// backend (such as Jaeger, Zipkin, or any OpenTelemetry collector).
//
// Configuration in crush.json:
//
//	{
//	  "options": {
//	    "plugins": {
//	      "otlp": {
//	        "endpoint": "http://localhost:4318",
//	        "service_name": "crush",
//	        "insecure": true
//	      }
//	    }
//	  }
//	}
package otlp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/crush/plugin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	// HookName is the name of the OTLP hook.
	HookName = "otlp"

	// DefaultServiceName is used when no service name is configured.
	DefaultServiceName = "crush"

	// DefaultEndpoint is the default OTLP HTTP endpoint.
	DefaultEndpoint = "http://localhost:4318"

	// DefaultContentLimit is the max length for message content attributes.
	DefaultContentLimit = 4000

	// DefaultToolInputLimit is the max length for tool input attributes.
	DefaultToolInputLimit = 4000

	// DefaultToolResultLimit is the max length for tool result attributes.
	DefaultToolResultLimit = 4000
)

// Config defines the configuration options for the OTLP plugin.
type Config struct {
	// Endpoint is the OTLP HTTP endpoint (e.g., "http://localhost:4318").
	Endpoint string `json:"endpoint,omitempty"`

	// ServiceName is the service name reported in traces.
	ServiceName string `json:"service_name,omitempty"`

	// Insecure allows HTTP connections instead of HTTPS.
	Insecure bool `json:"insecure,omitempty"`

	// Headers to include with OTLP requests.
	Headers map[string]string `json:"headers,omitempty"`

	// ContentLimit is the max length for message content attributes (default: 4000).
	ContentLimit int `json:"content_limit,omitempty"`

	// ToolInputLimit is the max length for tool input attributes (default: 4000).
	ToolInputLimit int `json:"tool_input_limit,omitempty"`

	// ToolResultLimit is the max length for tool result attributes (default: 4000).
	ToolResultLimit int `json:"tool_result_limit,omitempty"`
}

func init() {
	plugin.RegisterHookWithConfig(HookName, func(ctx context.Context, app *plugin.App) (plugin.Hook, error) {
		var cfg Config
		if err := app.LoadConfig(HookName, &cfg); err != nil {
			return nil, err
		}
		return NewOTLPHook(app, cfg)
	}, &Config{})
}

// gitInfo holds git repository information.
type gitInfo struct {
	repo   string
	branch string
}

// sessionContext holds both a session span and its context for proper parent-child relationships.
type sessionContext struct {
	span trace.Span
	ctx  context.Context
}

// OTLPHook implements the plugin.Hook interface for OTLP tracing.
type OTLPHook struct {
	app      *plugin.App
	cfg      Config
	tracer   trace.Tracer
	provider *sdktrace.TracerProvider
	logger   *slog.Logger

	// sessionContexts tracks active session spans and their contexts by session ID.
	sessionContexts   map[string]sessionContext
	sessionContextsMu sync.RWMutex

	// toolSpans tracks active tool call spans by tool call ID.
	toolSpans   map[string]trace.Span
	toolSpansMu sync.RWMutex

	// completedAssistantMessages tracks message IDs that have already had spans created.
	// This prevents duplicate spans when MessageUpdated is called multiple times.
	completedAssistantMessages   map[string]struct{}
	completedAssistantMessagesMu sync.RWMutex

	// Cached project/git info.
	projectPath string
	projectName string
	gitInfoVal  *gitInfo
}

// NewOTLPHook creates a new OTLP tracing hook.
func NewOTLPHook(app *plugin.App, cfg Config) (*OTLPHook, error) {
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = DefaultServiceName
	}
	if cfg.ContentLimit == 0 {
		cfg.ContentLimit = DefaultContentLimit
	}
	if cfg.ToolInputLimit == 0 {
		cfg.ToolInputLimit = DefaultToolInputLimit
	}
	if cfg.ToolResultLimit == 0 {
		cfg.ToolResultLimit = DefaultToolResultLimit
	}

	hook := &OTLPHook{
		app:                        app,
		cfg:                        cfg,
		logger:                     app.Logger().With("hook", HookName),
		sessionContexts:            make(map[string]sessionContext),
		toolSpans:                  make(map[string]trace.Span),
		completedAssistantMessages: make(map[string]struct{}),
	}

	// Initialize project info.
	hook.initProjectInfo()

	return hook, nil
}

// initProjectInfo populates project and git info from working directory.
func (h *OTLPHook) initProjectInfo() {
	h.projectPath = h.app.WorkingDir()
	if h.projectPath != "" {
		h.projectName = filepath.Base(h.projectPath)
	}
	h.gitInfoVal = getGitInfo(h.projectPath)
}

// getGitInfo returns git repository info or nil if not a git repo.
func getGitInfo(dir string) *gitInfo {
	if dir == "" {
		return nil
	}

	// Check if .git exists.
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return nil
	}

	info := &gitInfo{}

	// Get remote origin URL.
	if out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output(); err == nil {
		info.repo = normalizeGitURL(strings.TrimSpace(string(out)))
	}

	// Get current branch.
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		info.branch = strings.TrimSpace(string(out))
	}

	if info.repo == "" && info.branch == "" {
		return nil
	}
	return info
}

// normalizeGitURL converts git SSH/HTTP URLs to a normalized form.
func normalizeGitURL(url string) string {
	// Remove .git suffix.
	url = strings.TrimSuffix(url, ".git")

	// Convert SSH URLs (git@github.com:user/repo) to normalized form (github.com/user/repo).
	if after, found := strings.CutPrefix(url, "git@"); found {
		url = strings.Replace(after, ":", "/", 1)
	}

	// Remove protocol prefixes.
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")

	return url
}

// Name returns the hook identifier.
func (h *OTLPHook) Name() string {
	return HookName
}

// Start begins processing message events and exporting traces.
func (h *OTLPHook) Start(ctx context.Context) error {
	// Initialize OTLP exporter.
	if err := h.initTracer(ctx); err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}

	messages := h.app.Messages()
	if messages == nil {
		h.logger.Warn("no message subscriber available, OTLP tracing disabled")
		return nil
	}

	events := messages.SubscribeMessages(ctx)
	h.logger.Info("OTLP tracing started", "endpoint", h.cfg.Endpoint, "service", h.cfg.ServiceName)

	for {
		select {
		case <-ctx.Done():
			return h.Stop()
		case event, ok := <-events:
			if !ok {
				// Events channel closed - ensure spans are properly ended.
				return h.Stop()
			}
			h.handleEvent(ctx, event)
		}
	}
}

// Stop gracefully shuts down the hook.
func (h *OTLPHook) Stop() error {
	if h.provider == nil {
		return nil
	}

	// End all session spans with end reason.
	h.sessionContextsMu.Lock()
	for _, sc := range h.sessionContexts {
		sc.span.SetAttributes(attribute.String("session.end_reason", "user_exit"))
		sc.span.End()
	}
	h.sessionContexts = make(map[string]sessionContext)
	h.sessionContextsMu.Unlock()

	// End any remaining active tool spans.
	h.toolSpansMu.Lock()
	for _, span := range h.toolSpans {
		span.End()
	}
	h.toolSpans = make(map[string]trace.Span)
	h.toolSpansMu.Unlock()

	// Clear completed assistant messages tracker.
	h.completedAssistantMessagesMu.Lock()
	h.completedAssistantMessages = make(map[string]struct{})
	h.completedAssistantMessagesMu.Unlock()

	// Shutdown the tracer provider.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.provider.Shutdown(ctx); err != nil {
		h.logger.Error("failed to shutdown tracer provider", "error", err)
		return err
	}

	h.logger.Info("OTLP tracing stopped")
	return nil
}

func (h *OTLPHook) initTracer(ctx context.Context) error {
	var opts []otlptracehttp.Option

	opts = append(opts, otlptracehttp.WithEndpointURL(h.cfg.Endpoint))

	if h.cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	if len(h.cfg.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(h.cfg.Headers))
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(h.cfg.ServiceName),
			attribute.String("crush.version", "1.0.0"),
			attribute.String("agent.name", "crush"),
			attribute.String("agent.type", "coding-assistant"),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}

	h.provider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(h.provider)
	h.tracer = h.provider.Tracer("crush.agent")

	return nil
}

func (h *OTLPHook) handleEvent(ctx context.Context, event plugin.MessageEvent) {
	msg := event.Message

	switch event.Type {
	case plugin.MessageCreated:
		h.handleMessageCreated(ctx, msg)
	case plugin.MessageUpdated:
		h.handleMessageUpdated(ctx, msg)
	case plugin.MessageDeleted:
		h.handleMessageDeleted(msg)
	}
}

func (h *OTLPHook) handleMessageCreated(ctx context.Context, msg plugin.Message) {
	// Get or create session context with proper parent-child relationship.
	sessionCtx := h.getOrCreateSessionContext(ctx, msg.SessionID)

	switch msg.Role {
	case plugin.MessageRoleUser:
		h.createUserMessageSpan(sessionCtx, msg)
	case plugin.MessageRoleAssistant:
		// Don't create span on MessageCreated - wait for MessageUpdated when complete.
		// Streaming responses arrive via updates, so the initial create has no content.
	case plugin.MessageRoleTool:
		h.handleToolResults(sessionCtx, msg)
	}
}

func (h *OTLPHook) handleMessageUpdated(ctx context.Context, msg plugin.Message) {
	if msg.Role != plugin.MessageRoleAssistant {
		return
	}

	sessionCtx := h.getOrCreateSessionContext(ctx, msg.SessionID)

	// Handle tool calls.
	for _, tc := range msg.ToolCalls {
		if tc.Finished {
			// Tool call is complete - either end existing span or create+end if new.
			h.finishToolCallSpan(sessionCtx, tc, msg.SessionID)
		} else {
			h.createToolCallSpan(sessionCtx, tc, msg.SessionID)
		}
	}

	// Create assistant message span only when message is complete.
	h.maybeCreateAssistantMessageSpan(sessionCtx, msg)
}

func (h *OTLPHook) handleMessageDeleted(msg plugin.Message) {
	// Clean up any associated spans.
	for _, tc := range msg.ToolCalls {
		h.endToolCallSpan(tc)
	}
}

// getOrCreateSessionContext returns the context with the session span as parent.
// This ensures all child spans (messages, tools) are properly linked to the session.
func (h *OTLPHook) getOrCreateSessionContext(ctx context.Context, sessionID string) context.Context {
	h.sessionContextsMu.RLock()
	sc, exists := h.sessionContexts[sessionID]
	h.sessionContextsMu.RUnlock()

	if exists {
		return sc.ctx
	}

	h.sessionContextsMu.Lock()
	defer h.sessionContextsMu.Unlock()

	// Double-check after acquiring write lock.
	if sc, exists = h.sessionContexts[sessionID]; exists {
		return sc.ctx
	}

	// Build session attributes with required fields.
	// Per spec, project.path and project.name are required, so always include them.
	projectPath := h.projectPath
	if projectPath == "" {
		projectPath = "unknown"
	}
	projectName := h.projectName
	if projectName == "" {
		projectName = "unknown"
	}

	attrs := []attribute.KeyValue{
		attribute.String("session.id", sessionID),
		attribute.String("session.start_reason", "user_initiated"),
		attribute.String("agent.name", "crush"),
		attribute.String("project.path", projectPath),
		attribute.String("project.name", projectName),
	}

	// Add git info.
	if h.gitInfoVal != nil {
		if h.gitInfoVal.repo != "" {
			attrs = append(attrs, attribute.String("git.repo", h.gitInfoVal.repo))
		}
		if h.gitInfoVal.branch != "" {
			attrs = append(attrs, attribute.String("git.branch", h.gitInfoVal.branch))
		}
	}

	// Add LLM model info from session info provider.
	if sip := h.app.SessionInfo(); sip != nil {
		if info := sip.SessionInfo(); info != nil {
			if info.Model != "" {
				attrs = append(attrs, attribute.String("llm.model", info.Model))
			}
			if info.Provider != "" {
				attrs = append(attrs, attribute.String("llm.provider", info.Provider))
			}
		}
	}

	// Create a new root span for this session.
	// Use trace.WithNewRoot() to ensure this is a trace root, not a child of any existing span.
	sessionCtx, span := h.tracer.Start(ctx, "crush.session",
		trace.WithNewRoot(),
		trace.WithAttributes(attrs...),
	)

	// Session span is kept open until the session ends or Stop() is called.
	// This ensures session duration properly reflects actual session length.

	h.sessionContexts[sessionID] = sessionContext{span: span, ctx: sessionCtx}
	return sessionCtx
}

func (h *OTLPHook) createUserMessageSpan(ctx context.Context, msg plugin.Message) {
	_, span := h.tracer.Start(ctx, "crush.message.user",
		trace.WithAttributes(
			attribute.String("message.id", msg.ID),
			attribute.String("message.role", string(msg.Role)),
			attribute.String("session.id", msg.SessionID),
			attribute.Int("message.content_length", len(msg.Content)),
		),
	)

	// Add content as attribute (truncated if too long).
	content := truncateString(msg.Content, h.cfg.ContentLimit)
	span.SetAttributes(attribute.String("message.content", content))

	// User messages are instant, end immediately.
	span.End()
}

func (h *OTLPHook) maybeCreateAssistantMessageSpan(ctx context.Context, msg plugin.Message) {
	// Check if message is complete.
	// A message is complete when it has content and all tool calls are finished.
	allToolsFinished := true
	for _, tc := range msg.ToolCalls {
		if !tc.Finished {
			allToolsFinished = false
			break
		}
	}

	// Only create span when message is complete: has content AND all tools finished.
	if msg.Content == "" || !allToolsFinished {
		return
	}

	// Check if we've already created a span for this message.
	h.completedAssistantMessagesMu.Lock()
	if _, exists := h.completedAssistantMessages[msg.ID]; exists {
		h.completedAssistantMessagesMu.Unlock()
		return
	}
	h.completedAssistantMessages[msg.ID] = struct{}{}
	h.completedAssistantMessagesMu.Unlock()

	// Build attributes.
	attrs := []attribute.KeyValue{
		attribute.String("message.id", msg.ID),
		attribute.String("message.role", string(msg.Role)),
		attribute.String("session.id", msg.SessionID),
		attribute.Int("message.content_length", len(msg.Content)),
	}

	// Add LLM metrics from session info.
	if sip := h.app.SessionInfo(); sip != nil {
		if info := sip.SessionInfo(); info != nil {
			if info.Model != "" {
				attrs = append(attrs, attribute.String("llm.model", info.Model))
			}
			if info.Provider != "" {
				attrs = append(attrs, attribute.String("llm.provider", info.Provider))
			}
			attrs = append(attrs,
				attribute.Int64("llm.tokens.input", info.Tokens.Input),
				attribute.Int64("llm.tokens.output", info.Tokens.Output),
				attribute.Int64("llm.tokens.cache_read", info.Tokens.CacheRead),
				attribute.Int64("llm.tokens.cache_write", info.Tokens.CacheWrite),
				attribute.Float64("llm.cost_usd", info.CostUSD),
			)
		}
	}

	// Create and immediately end the span with final content.
	_, span := h.tracer.Start(ctx, "crush.message.assistant",
		trace.WithAttributes(attrs...),
	)

	// Add content (truncated if too long).
	content := truncateString(msg.Content, h.cfg.ContentLimit)
	span.SetAttributes(attribute.String("message.content", content))

	// Add tool call count if any.
	if len(msg.ToolCalls) > 0 {
		span.SetAttributes(attribute.Int("message.tool_calls", len(msg.ToolCalls)))
	}

	span.End()
}

func (h *OTLPHook) createToolCallSpan(ctx context.Context, tc plugin.ToolCallInfo, sessionID string) {
	h.toolSpansMu.Lock()
	defer h.toolSpansMu.Unlock()

	// Don't create duplicate spans.
	if _, exists := h.toolSpans[tc.ID]; exists {
		return
	}

	attrs := []attribute.KeyValue{
		attribute.String("tool.id", tc.ID),
		attribute.String("tool.name", tc.Name),
		attribute.String("session.id", sessionID),
		attribute.Bool("tool.is_error", false), // Will be updated when tool finishes
	}

	// Only add input if available (may be empty for streaming tool calls).
	if tc.Input != "" {
		input := truncateString(tc.Input, h.cfg.ToolInputLimit)
		attrs = append(attrs, attribute.String("tool.input", input))
	}

	_, span := h.tracer.Start(ctx, "crush.tool."+tc.Name,
		trace.WithAttributes(attrs...),
	)

	// Parse JSON input and add individual parameters as attributes.
	if tc.Input != "" {
		h.addToolParamsToSpan(span, tc.Input)
	}

	h.toolSpans[tc.ID] = span
}

// addToolParamsToSpan parses JSON tool input and adds individual parameters as span attributes.
// It also extracts semantic attributes like target files and URLs.
func (h *OTLPHook) addToolParamsToSpan(span trace.Span, input string) {
	var params map[string]any
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return // Not valid JSON, skip parameter extraction
	}

	// Extract semantic attributes based on common tool patterns.
	if filePath, ok := params["file_path"].(string); ok {
		span.SetAttributes(attribute.String("tool.target_file", filePath))
	}
	if path, ok := params["path"].(string); ok && isFilePath(path) {
		span.SetAttributes(attribute.String("tool.target_file", path))
	}
	if url, ok := params["url"].(string); ok {
		span.SetAttributes(attribute.String("tool.target_url", url))
	}
	if pattern, ok := params["pattern"].(string); ok {
		span.SetAttributes(attribute.String("tool.search_pattern", pattern))
	}
	if command, ok := params["command"].(string); ok {
		span.SetAttributes(attribute.String("tool.command", truncateString(command, 500)))
	}

	for key, value := range params {
		attrKey := "tool.param." + key
		switch v := value.(type) {
		case string:
			// Truncate long string values.
			span.SetAttributes(attribute.String(attrKey, truncateString(v, 500)))
		case float64:
			// JSON numbers are float64.
			span.SetAttributes(attribute.Float64(attrKey, v))
		case bool:
			span.SetAttributes(attribute.Bool(attrKey, v))
		case nil:
			span.SetAttributes(attribute.String(attrKey, "null"))
		default:
			// For arrays and objects, marshal back to JSON string.
			if jsonBytes, err := json.Marshal(v); err == nil {
				jsonStr := truncateString(string(jsonBytes), 500)
				span.SetAttributes(attribute.String(attrKey, jsonStr))
			}
		}
	}
}

// isFilePath checks if a string looks like a file path.
func isFilePath(s string) bool {
	return strings.HasPrefix(s, "/") ||
		strings.HasPrefix(s, "./") ||
		strings.HasPrefix(s, "../") ||
		strings.Contains(s, "/")
}

func (h *OTLPHook) endToolCallSpan(tc plugin.ToolCallInfo) {
	h.toolSpansMu.Lock()
	defer h.toolSpansMu.Unlock()

	if span, exists := h.toolSpans[tc.ID]; exists {
		// When the tool finishes, the input is finally available.
		// Add it now since it wasn't available when the span was created.
		if tc.Input != "" {
			input := truncateString(tc.Input, h.cfg.ToolInputLimit)
			span.SetAttributes(attribute.String("tool.input", input))
			h.addToolParamsToSpan(span, tc.Input)
		}
		// Note: tool.is_error will be set by handleToolResults if a result arrives.
		span.End()
		delete(h.toolSpans, tc.ID)
	}
}

// finishToolCallSpan completes a tool call span. If the span exists, it updates it with
// input and ends it. If the span doesn't exist (tool call arrived already finished),
// it creates a new span with the input and immediately ends it.
func (h *OTLPHook) finishToolCallSpan(ctx context.Context, tc plugin.ToolCallInfo, sessionID string) {
	h.toolSpansMu.Lock()
	defer h.toolSpansMu.Unlock()

	span, exists := h.toolSpans[tc.ID]
	if !exists {
		// Tool call arrived already finished - create span now with the input.
		attrs := []attribute.KeyValue{
			attribute.String("tool.id", tc.ID),
			attribute.String("tool.name", tc.Name),
			attribute.String("session.id", sessionID),
			attribute.Bool("tool.is_error", false), // Default to false, will be updated by tool result
		}

		// Add input if available.
		if tc.Input != "" {
			input := truncateString(tc.Input, h.cfg.ToolInputLimit)
			attrs = append(attrs, attribute.String("tool.input", input))
		}

		_, span = h.tracer.Start(ctx, "crush.tool."+tc.Name,
			trace.WithAttributes(attrs...),
		)

		// Parse JSON input and add individual parameters as attributes.
		if tc.Input != "" {
			h.addToolParamsToSpan(span, tc.Input)
		}
	} else {
		// Existing span - add input if available (may not have been set at creation time).
		if tc.Input != "" {
			input := truncateString(tc.Input, h.cfg.ToolInputLimit)
			span.SetAttributes(attribute.String("tool.input", input))
			h.addToolParamsToSpan(span, tc.Input)
		}
	}

	span.End()

	// Clean up if it was in the map.
	if exists {
		delete(h.toolSpans, tc.ID)
	}
}

// endToolCallSpanByID ends a tool span by ID only (used when we don't have the input).
func (h *OTLPHook) endToolCallSpanByID(toolCallID string) {
	h.toolSpansMu.Lock()
	defer h.toolSpansMu.Unlock()

	if span, exists := h.toolSpans[toolCallID]; exists {
		span.End()
		delete(h.toolSpans, toolCallID)
	}
}

func (h *OTLPHook) handleToolResults(ctx context.Context, msg plugin.Message) {
	for _, tr := range msg.ToolResults {
		h.toolSpansMu.Lock()
		span, exists := h.toolSpans[tr.ToolCallID]
		h.toolSpansMu.Unlock()

		if exists {
			// Add result to the span.
			content := truncateString(tr.Content, h.cfg.ToolResultLimit)
			span.SetAttributes(
				attribute.String("tool.result", content),
				attribute.Int("tool.result_length", len(tr.Content)),
				attribute.Bool("tool.is_error", tr.IsError),
			)
			h.endToolCallSpanByID(tr.ToolCallID)
		} else {
			// Create a new span for orphaned tool results.
			_, resultSpan := h.tracer.Start(ctx, "crush.tool."+tr.Name,
				trace.WithAttributes(
					attribute.String("tool.id", tr.ToolCallID),
					attribute.String("tool.name", tr.Name),
					attribute.String("session.id", msg.SessionID),
					attribute.Bool("tool.is_error", tr.IsError),
				),
			)

			content := truncateString(tr.Content, h.cfg.ToolResultLimit)
			resultSpan.SetAttributes(
				attribute.String("tool.result", content),
				attribute.Int("tool.result_length", len(tr.Content)),
			)
			resultSpan.End()
		}
	}
}

// truncateString truncates a string to the specified limit, adding "..." if truncated.
func truncateString(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
