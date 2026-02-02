// Package mockllm provides a mock LLM server for E2E testing.
//
// It implements an OpenAI-compatible chat completions API that can be configured
// to return specific responses based on message patterns or sequences.
//
// Basic usage:
//
//	server := mockllm.NewServer()
//	server.OnMessage("hello", mockllm.TextResponse("Hello! How can I help?"))
//	server.Start(t)
//	defer server.Close()
//
//	// Configure crush to use server.URL() as the provider base URL
//
// Tool call example:
//
//	server := mockllm.NewServer()
//	server.OnAny(mockllm.ToolCall("ping", `{}`))
//	server.OnToolResult("ping", mockllm.TextResponse("Pong received!"))
//	server.Start(t)
package mockllm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Server is a mock OpenAI-compatible LLM server.
type Server struct {
	httpServer *httptest.Server
	mu         sync.RWMutex
	t          *testing.T

	// Response handlers.
	handlers       []handler
	defaultHandler ResponseFunc
	callSequence   []ResponseFunc
	callIndex      int

	// Request logging.
	requests []Request
}

// Request represents a captured request to the mock server.
type Request struct {
	Method    string
	Path      string
	Body      ChatRequest
	Timestamp time.Time
}

// handler matches requests and returns responses.
type handler struct {
	matcher MatchFunc
	respond ResponseFunc
}

// MatchFunc determines if a handler should respond to a request.
type MatchFunc func(req ChatRequest) bool

// ResponseFunc generates a response for a request.
type ResponseFunc func(req *ChatRequest) *ChatResponse

// NewServer creates a new mock LLM server.
func NewServer() *Server {
	return &Server{
		defaultHandler: func(req *ChatRequest) *ChatResponse {
			return TextResponse("I don't know how to respond to that.")(req)
		},
	}
}

// Start starts the HTTP server and returns its URL.
func (s *Server) Start(t *testing.T) string {
	s.t = t
	s.httpServer = httptest.NewServer(http.HandlerFunc(s.handleRequest))
	t.Cleanup(s.Close)
	return s.httpServer.URL
}

// Close shuts down the server.
func (s *Server) Close() {
	if s.httpServer != nil {
		s.httpServer.Close()
	}
}

// URL returns the server's base URL.
func (s *Server) URL() string {
	if s.httpServer == nil {
		return ""
	}
	return s.httpServer.URL
}

// Requests returns all captured requests.
func (s *Server) Requests() []Request {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Request{}, s.requests...)
}

// LastRequest returns the most recent request.
func (s *Server) LastRequest() *Request {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.requests) == 0 {
		return nil
	}
	return &s.requests[len(s.requests)-1]
}

// Reset clears all handlers and request history.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers = nil
	s.callSequence = nil
	s.callIndex = 0
	s.requests = nil
}

// OnMessage adds a handler that matches when the last user message contains the text.
func (s *Server) OnMessage(contains string, respond ResponseFunc) *Server {
	return s.On(MessageContains(contains), respond)
}

// OnToolResult adds a handler that matches when there's a tool result with the given name.
func (s *Server) OnToolResult(toolName string, respond ResponseFunc) *Server {
	return s.On(HasToolResult(toolName), respond)
}

// OnAny adds a handler that matches any request.
func (s *Server) OnAny(respond ResponseFunc) *Server {
	return s.On(func(req ChatRequest) bool { return true }, respond)
}

// On adds a custom handler with a matcher.
func (s *Server) On(match MatchFunc, respond ResponseFunc) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers = append(s.handlers, handler{match, respond})
	return s
}

// Sequence configures the server to return responses in order.
// Each call to the server returns the next response in the sequence.
func (s *Server) Sequence(responses ...ResponseFunc) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callSequence = responses
	s.callIndex = 0
	return s
}

// Default sets the default response when no handlers match.
func (s *Server) Default(respond ResponseFunc) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultHandler = respond
	return s
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Only handle chat completions endpoint.
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Log the request.
	s.mu.Lock()
	s.requests = append(s.requests, Request{
		Method:    r.Method,
		Path:      r.URL.Path,
		Body:      req,
		Timestamp: time.Now(),
	})
	s.mu.Unlock()

	// Find a handler.
	resp := s.findResponse(&req)

	// Check if streaming is requested.
	if req.Stream {
		s.sendStreamResponse(w, resp)
	} else {
		s.sendJSONResponse(w, resp)
	}
}

func (s *Server) findResponse(req *ChatRequest) *ChatResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check sequence first.
	if len(s.callSequence) > 0 {
		if s.callIndex < len(s.callSequence) {
			resp := s.callSequence[s.callIndex](req)
			s.callIndex++
			return resp
		}
		// Sequence exhausted, use default.
		return s.defaultHandler(req)
	}

	// Check handlers in reverse order (last added wins).
	for i := len(s.handlers) - 1; i >= 0; i-- {
		h := s.handlers[i]
		if h.matcher(*req) {
			return h.respond(req)
		}
	}

	return s.defaultHandler(req)
}

func (s *Server) sendJSONResponse(w http.ResponseWriter, resp *ChatResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil && s.t != nil {
		s.t.Logf("mockllm: failed to encode response: %v", err)
	}
}

func (s *Server) sendStreamResponse(w http.ResponseWriter, resp *ChatResponse) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Convert response to stream chunks.
	chunks := responseToStreamChunks(resp)
	for _, chunk := range chunks {
		data, err := json.Marshal(chunk)
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		time.Sleep(10 * time.Millisecond) // Simulate realistic streaming
	}

	// Send done marker.
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func responseToStreamChunks(resp *ChatResponse) []StreamChunk {
	var chunks []StreamChunk

	if len(resp.Choices) == 0 {
		return chunks
	}

	choice := resp.Choices[0]

	// If there's content, stream it character by character (or in small chunks).
	if choice.Message.Content != "" {
		content := choice.Message.Content
		// Stream in chunks of ~20 chars for realistic simulation.
		for i := 0; i < len(content); i += 20 {
			end := i + 20
			if end > len(content) {
				end = len(content)
			}
			chunks = append(chunks, StreamChunk{
				ID:      resp.ID,
				Object:  "chat.completion.chunk",
				Model:   resp.Model,
				Created: resp.Created,
				Choices: []StreamChoice{{
					Index: 0,
					Delta: Delta{Content: content[i:end]},
				}},
			})
		}
	}

	// Stream tool calls.
	for _, tc := range choice.Message.ToolCalls {
		// Tool call start.
		chunks = append(chunks, StreamChunk{
			ID:      resp.ID,
			Object:  "chat.completion.chunk",
			Model:   resp.Model,
			Created: resp.Created,
			Choices: []StreamChoice{{
				Index: 0,
				Delta: Delta{
					ToolCalls: []ToolCallDelta{{
						Index: 0,
						ID:    tc.ID,
						Type:  tc.Type,
						Function: FunctionDelta{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					}},
				},
			}},
		})
	}

	// Final chunk with finish reason.
	finishReason := choice.FinishReason
	if finishReason == "" {
		if len(choice.Message.ToolCalls) > 0 {
			finishReason = "tool_calls"
		} else {
			finishReason = "stop"
		}
	}
	chunks = append(chunks, StreamChunk{
		ID:      resp.ID,
		Object:  "chat.completion.chunk",
		Model:   resp.Model,
		Created: resp.Created,
		Choices: []StreamChoice{{
			Index:        0,
			Delta:        Delta{},
			FinishReason: finishReason,
		}},
		Usage: resp.Usage,
	})

	return chunks
}

// ParseSSEStream parses an SSE stream and returns chunks.
// Useful for testing streaming responses.
func ParseSSEStream(r io.Reader) ([]StreamChunk, error) {
	var chunks []StreamChunk
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, fmt.Errorf("failed to parse chunk: %w", err)
		}
		chunks = append(chunks, chunk)
	}

	return chunks, scanner.Err()
}
