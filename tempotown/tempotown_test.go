package tempotown

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mockMCPServer simulates a Tempotown MCP server for testing.
type mockMCPServer struct {
	listener net.Listener
	handlers map[string]func(json.RawMessage) (any, error)
	mu       sync.Mutex
	calls    []string
}

func newMockMCPServer(t *testing.T) *mockMCPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	s := &mockMCPServer{
		listener: listener,
		handlers: make(map[string]func(json.RawMessage) (any, error)),
	}

	// Default handlers.
	s.handlers["initialize"] = func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]string{"name": "mock-tempotown", "version": "0.1.0"},
			"capabilities":    map[string]any{"tools": map[string]bool{"listChanged": true}},
		}, nil
	}

	s.handlers["tools/call"] = func(params json.RawMessage) (any, error) {
		var p ToolCallParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}

		s.mu.Lock()
		s.calls = append(s.calls, p.Name)
		s.mu.Unlock()

		switch p.Name {
		case "register_agent":
			return map[string]any{
				"content": []map[string]string{{"type": "text", "text": `{"agent_id":"test-agent-123"}`}},
			}, nil
		case "report_status":
			return map[string]any{
				"content": []map[string]string{{"type": "text", "text": `{"ok":true}`}},
			}, nil
		case "get_pending_feedback":
			return map[string]any{
				"content": []map[string]string{{"type": "text", "text": `{"items":[]}`}},
			}, nil
		default:
			return map[string]any{
				"content": []map[string]string{{"type": "text", "text": `{}`}},
			}, nil
		}
	}

	go s.serve()
	return s
}

func (s *mockMCPServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *mockMCPServer) handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	decoder := json.NewDecoder(reader)
	encoder := json.NewEncoder(conn)

	for {
		var req Request
		if err := decoder.Decode(&req); err != nil {
			return
		}

		// Notifications have no ID.
		if req.ID == nil {
			continue
		}

		handler, ok := s.handlers[req.Method]
		if !ok {
			resp := Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &Error{Code: -32601, Message: "method not found"},
			}
			encoder.Encode(resp)
			continue
		}

		result, err := handler(req.Params)
		if err != nil {
			resp := Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &Error{Code: -32000, Message: err.Error()},
			}
			encoder.Encode(resp)
			continue
		}

		resultJSON, _ := json.Marshal(result)
		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  resultJSON,
		}
		encoder.Encode(resp)
	}
}

func (s *mockMCPServer) addr() string {
	return s.listener.Addr().String()
}

func (s *mockMCPServer) close() {
	s.listener.Close()
}

func (s *mockMCPServer) getCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]string, len(s.calls))
	copy(result, s.calls)
	return result
}

func TestNewTempotownHook(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Endpoint:     "localhost:9999",
		Role:         "reviewer",
		Capabilities: []string{"review", "test"},
	}

	hook, err := NewTempotownHook(nil, cfg)
	require.NoError(t, err)
	require.NotNil(t, hook)
	require.Equal(t, HookName, hook.Name())
	require.Equal(t, "localhost:9999", hook.cfg.Endpoint)
	require.Equal(t, "reviewer", hook.cfg.Role)
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := Config{}
	hook, err := NewTempotownHook(nil, cfg)
	require.NoError(t, err)
	require.Equal(t, DefaultEndpoint, hook.cfg.Endpoint)
	require.Equal(t, DefaultRole, hook.cfg.Role)
	require.Equal(t, int(DefaultPollInterval/time.Second), hook.cfg.PollIntervalSeconds)
}

func TestConnect(t *testing.T) {
	t.Parallel()

	server := newMockMCPServer(t)
	defer server.close()

	cfg := Config{
		Endpoint: server.addr(),
		Role:     "coder",
	}

	hook, err := NewTempotownHook(nil, cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = hook.connect(ctx)
	require.NoError(t, err)
	require.True(t, hook.IsConnected())
	require.Equal(t, "test-agent-123", hook.agentID)

	// Verify register_agent was called.
	calls := server.getCalls()
	require.Contains(t, calls, "register_agent")
}

func TestConnectFailure(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Endpoint: "localhost:1", // Invalid port.
	}

	hook, err := NewTempotownHook(nil, cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = hook.connect(ctx)
	require.Error(t, err)
	require.False(t, hook.IsConnected())
}

func TestCallTool(t *testing.T) {
	t.Parallel()

	server := newMockMCPServer(t)
	defer server.close()

	cfg := Config{
		Endpoint: server.addr(),
	}

	hook, err := NewTempotownHook(nil, cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = hook.connect(ctx)
	require.NoError(t, err)

	// Call report_status.
	_, err = hook.callTool(ctx, "report_status", map[string]any{
		"status":   "testing",
		"progress": 50,
	})
	require.NoError(t, err)

	calls := server.getCalls()
	require.Contains(t, calls, "report_status")
}

func TestFeedbackChannel(t *testing.T) {
	t.Parallel()

	cfg := Config{}
	hook, err := NewTempotownHook(nil, cfg)
	require.NoError(t, err)

	ch := hook.FeedbackCh()
	require.NotNil(t, ch)

	// Channel should be buffered.
	select {
	case hook.feedbackCh <- FeedbackPayload{Message: "test"}:
	default:
		t.Fatal("channel should accept messages")
	}

	select {
	case fb := <-ch:
		require.Equal(t, "test", fb.Message)
	default:
		t.Fatal("should receive from channel")
	}
}
