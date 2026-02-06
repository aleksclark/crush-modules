package tempotown_test

import (
	"bufio"
	"encoding/json"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aleksclark/crush-modules/testutil"
	"github.com/aleksclark/crush-modules/testutil/mockllm"
	"github.com/stretchr/testify/require"
)

// TestTempotownPluginRegistered verifies the tempotown hook is registered in the distro.
func TestTempotownPluginRegistered(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	// Run crush with --list-plugins flag.
	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	require.Contains(t, output, "tempotown", "Expected tempotown hook to be registered")
	require.Contains(t, output, "Registered plugin hooks", "Expected hooks list header")
}

// TestTempotownGracefulDegradation verifies Crush works when Tempotown is unavailable.
func TestTempotownGracefulDegradation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	// Start mock LLM server.
	llmServer := mockllm.NewServer()
	llmServer.OnMessage("hello", mockllm.TextResponse("Hello! How can I help you?"))
	llmURL := llmServer.Start(t)

	// Configure tempotown to connect to an unavailable endpoint.
	config := map[string]any{
		"options": map[string]any{
			"plugins": map[string]any{
				"tempotown": map[string]any{
					"endpoint": "localhost:19999", // Port that doesn't exist
					"role":     "coder",
				},
			},
		},
	}
	tmpDir := mockllm.SetupTestEnvWithConfig(t, llmURL, config)

	// Start crush - should work despite Tempotown being unavailable.
	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	// Wait for UI to be ready.
	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	// Send a message - Crush should respond normally.
	term.SendText("hello\r")

	// Wait for the response.
	require.True(t, testutil.WaitForText(t, term, "Hello", 15*time.Second),
		"Expected assistant response even with Tempotown unavailable")
}

// TestTempotownConnectsAndRegisters verifies the plugin connects to a mock MCP server.
func TestTempotownConnectsAndRegisters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	// Start mock Tempotown MCP server.
	mcpServer := newMockTempotownServer(t)
	defer mcpServer.close()

	// Start mock LLM server.
	llmServer := mockllm.NewServer()
	llmServer.OnMessage("hello", mockllm.TextResponse("Hello! I'm ready to help."))
	llmURL := llmServer.Start(t)

	// Configure tempotown to connect to our mock server.
	config := map[string]any{
		"options": map[string]any{
			"plugins": map[string]any{
				"tempotown": map[string]any{
					"endpoint":              mcpServer.addr(),
					"role":                  "coder",
					"capabilities":          []string{"code", "test"},
					"poll_interval_seconds": 1,
				},
			},
		},
	}
	tmpDir := mockllm.SetupTestEnvWithConfig(t, llmURL, config)

	// Start crush.
	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	// Wait for UI to be ready.
	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	// Wait for the tempotown plugin to connect and register.
	time.Sleep(2 * time.Second)

	// Verify register_agent was called.
	calls := mcpServer.getCalls()
	require.Contains(t, calls, "register_agent", "Expected tempotown plugin to call register_agent")
}

// TestTempotownReportsStatus verifies the plugin reports status during activity.
func TestTempotownReportsStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	// Start mock Tempotown MCP server.
	mcpServer := newMockTempotownServer(t)
	defer mcpServer.close()

	// Start mock LLM server.
	// Use OnAny to respond to any message (avoids matching issues).
	llmServer := mockllm.NewServer()
	llmServer.OnAny(mockllm.TextResponse("TASK_DONE: I've completed your request."))
	llmURL := llmServer.Start(t)

	// Configure tempotown.
	config := map[string]any{
		"options": map[string]any{
			"plugins": map[string]any{
				"tempotown": map[string]any{
					"endpoint":              mcpServer.addr(),
					"role":                  "coder",
					"poll_interval_seconds": 1,
				},
			},
		},
	}
	tmpDir := mockllm.SetupTestEnvWithConfig(t, llmURL, config)

	// Start crush.
	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	// Wait for UI to be ready.
	if !testutil.WaitForText(t, term, ">", 5*time.Second) {
		snap := term.Snapshot()
		t.Logf("Terminal output while waiting for >:\n%s", testutil.SnapshotText(snap))
		t.Fatal("UI should be ready")
	}

	// Wait for registration to complete.
	time.Sleep(1 * time.Second)

	// Clear call history to isolate status reporting calls.
	mcpServer.clearCalls()

	// Send a message to trigger status reporting.
	term.SendText("test message\r")

	// Wait for the response - look for our unique marker.
	if !testutil.WaitForText(t, term, "TASK_DONE", 15*time.Second) {
		snap := term.Snapshot()
		t.Logf("Terminal output:\n%s", testutil.SnapshotText(snap))
		t.Logf("LLM server received %d requests", len(llmServer.Requests()))
		for i, req := range llmServer.Requests() {
			t.Logf("Request %d: %s, messages=%d", i, req.Path, len(req.Body.Messages))
		}
		t.Fatal("Expected assistant response")
	}

	// Give time for async status reports.
	time.Sleep(500 * time.Millisecond)

	// Verify report_status was called.
	calls := mcpServer.getCalls()
	require.Contains(t, calls, "report_status", "Expected tempotown plugin to call report_status")
}

// mockTempotownServer simulates a Tempotown MCP server for e2e testing.
type mockTempotownServer struct {
	listener  net.Listener
	mu        sync.Mutex
	calls     []string
	connected atomic.Bool
}

func newMockTempotownServer(t *testing.T) *mockTempotownServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	s := &mockTempotownServer{
		listener: listener,
	}

	go s.serve()
	return s
}

func (s *mockTempotownServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.connected.Store(true)
		go s.handleConn(conn)
	}
}

func (s *mockTempotownServer) handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	decoder := json.NewDecoder(reader)
	encoder := json.NewEncoder(conn)

	for {
		var req map[string]any
		if err := decoder.Decode(&req); err != nil {
			return
		}

		method, _ := req["method"].(string)
		id := req["id"]

		// Notifications have no ID.
		if id == nil {
			continue
		}

		var result any
		switch method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]string{"name": "mock-tempotown", "version": "0.1.0"},
				"capabilities":    map[string]any{"tools": map[string]bool{"listChanged": true}},
			}

		case "tools/call":
			params, _ := req["params"].(map[string]any)
			toolName, _ := params["name"].(string)

			s.mu.Lock()
			s.calls = append(s.calls, toolName)
			s.mu.Unlock()

			var text string
			switch toolName {
			case "register_agent":
				text = `{"agent_id":"e2e-test-agent-123"}`
			case "report_status":
				text = `{"ok":true}`
			case "get_pending_feedback":
				text = `{"items":[]}`
			default:
				text = `{}`
			}

			result = map[string]any{
				"content": []map[string]string{{"type": "text", "text": text}},
			}

		default:
			// Unknown method - send error.
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			}
			encoder.Encode(resp)
			continue
		}

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  result,
		}
		encoder.Encode(resp)
	}
}

func (s *mockTempotownServer) addr() string {
	return s.listener.Addr().String()
}

func (s *mockTempotownServer) close() {
	s.listener.Close()
}

func (s *mockTempotownServer) getCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]string, len(s.calls))
	copy(result, s.calls)
	return result
}

func (s *mockTempotownServer) clearCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
}

func (s *mockTempotownServer) isConnected() bool {
	return s.connected.Load()
}
