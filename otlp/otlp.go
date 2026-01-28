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
	"fmt"
	"log/slog"
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
}

// NewOTLPHook creates a new OTLP tracing hook.
func NewOTLPHook(app *plugin.App, cfg Config) (*OTLPHook, error) {
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = DefaultServiceName
	}

	hook := &OTLPHook{
		app:             app,
		cfg:             cfg,
		logger:          app.Logger().With("hook", HookName),
		sessionContexts: make(map[string]sessionContext),
		toolSpans:       make(map[string]trace.Span),
	}

	return hook, nil
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
				return nil
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

	// Clear session contexts (session spans are already ended on creation).
	h.sessionContextsMu.Lock()
	h.sessionContexts = make(map[string]sessionContext)
	h.sessionContextsMu.Unlock()

	// End any remaining active tool spans.
	h.toolSpansMu.Lock()
	for _, span := range h.toolSpans {
		span.End()
	}
	h.toolSpans = make(map[string]trace.Span)
	h.toolSpansMu.Unlock()

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
		h.handleMessageDeleted(ctx, msg)
	}
}

func (h *OTLPHook) handleMessageCreated(ctx context.Context, msg plugin.Message) {
	// Get or create session context with proper parent-child relationship.
	sessionCtx := h.getOrCreateSessionContext(ctx, msg.SessionID)

	switch msg.Role {
	case plugin.MessageRoleUser:
		h.createUserMessageSpan(sessionCtx, msg)
	case plugin.MessageRoleAssistant:
		h.createAssistantMessageSpan(sessionCtx, msg)
	case plugin.MessageRoleTool:
		h.handleToolResults(sessionCtx, msg)
	}
}

func (h *OTLPHook) handleMessageUpdated(ctx context.Context, msg plugin.Message) {
	// For assistant messages with tool calls, create tool call spans.
	if msg.Role == plugin.MessageRoleAssistant && len(msg.ToolCalls) > 0 {
		sessionCtx := h.getOrCreateSessionContext(ctx, msg.SessionID)

		for _, tc := range msg.ToolCalls {
			if tc.Finished {
				h.endToolCallSpan(tc.ID)
			} else {
				h.createToolCallSpan(sessionCtx, tc, msg.SessionID)
			}
		}
	}
}

func (h *OTLPHook) handleMessageDeleted(ctx context.Context, msg plugin.Message) {
	// Clean up any associated spans.
	for _, tc := range msg.ToolCalls {
		h.endToolCallSpan(tc.ID)
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

	// Create a new root span for this session.
	// Use trace.WithNewRoot() to ensure this is a trace root, not a child of any existing span.
	sessionCtx, span := h.tracer.Start(ctx, "crush.session",
		trace.WithNewRoot(),
		trace.WithAttributes(
			attribute.String("session.id", sessionID),
		),
	)

	// End the session span immediately so it gets exported.
	// The context still carries the trace/span IDs, so child spans will be
	// properly parented to this session span even though it's already ended.
	// This is necessary because in interactive mode, sessions can be very
	// long-lived and we need the parent span to be visible in Jaeger before
	// the session ends.
	span.End()

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
	content := msg.Content
	if len(content) > 1000 {
		content = content[:1000] + "..."
	}
	span.SetAttributes(attribute.String("message.content", content))

	// User messages are instant, end immediately.
	span.End()
}

func (h *OTLPHook) createAssistantMessageSpan(ctx context.Context, msg plugin.Message) {
	_, span := h.tracer.Start(ctx, "crush.message.assistant",
		trace.WithAttributes(
			attribute.String("message.id", msg.ID),
			attribute.String("message.role", string(msg.Role)),
			attribute.String("session.id", msg.SessionID),
		),
	)

	// Add content if present.
	if msg.Content != "" {
		content := msg.Content
		if len(content) > 1000 {
			content = content[:1000] + "..."
		}
		span.SetAttributes(
			attribute.String("message.content", content),
			attribute.Int("message.content_length", len(msg.Content)),
		)
	}

	// Add tool call count.
	if len(msg.ToolCalls) > 0 {
		span.SetAttributes(attribute.Int("message.tool_calls", len(msg.ToolCalls)))
	}

	// End assistant message span (streaming updates are handled separately).
	span.End()
}

func (h *OTLPHook) createToolCallSpan(ctx context.Context, tc plugin.ToolCallInfo, sessionID string) {
	h.toolSpansMu.Lock()
	defer h.toolSpansMu.Unlock()

	// Don't create duplicate spans.
	if _, exists := h.toolSpans[tc.ID]; exists {
		return
	}

	_, span := h.tracer.Start(ctx, "crush.tool."+tc.Name,
		trace.WithAttributes(
			attribute.String("tool.id", tc.ID),
			attribute.String("tool.name", tc.Name),
			attribute.String("session.id", sessionID),
		),
	)

	// Add input as attribute (truncated if too long).
	input := tc.Input
	if len(input) > 2000 {
		input = input[:2000] + "..."
	}
	span.SetAttributes(attribute.String("tool.input", input))

	h.toolSpans[tc.ID] = span
}

func (h *OTLPHook) endToolCallSpan(toolCallID string) {
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
			content := tr.Content
			if len(content) > 2000 {
				content = content[:2000] + "..."
			}
			span.SetAttributes(
				attribute.String("tool.result", content),
				attribute.Bool("tool.is_error", tr.IsError),
			)
			h.endToolCallSpan(tr.ToolCallID)
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

			content := tr.Content
			if len(content) > 2000 {
				content = content[:2000] + "..."
			}
			resultSpan.SetAttributes(attribute.String("tool.result", content))
			resultSpan.End()
		}
	}
}
