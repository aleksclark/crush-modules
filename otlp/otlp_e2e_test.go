package otlp_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aleksclark/crush-modules/testutil"
	"github.com/aleksclark/crush-modules/testutil/mockllm"
	"github.com/stretchr/testify/require"
	tracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// mockOTLPReceiver is a simple OTLP HTTP receiver that captures traces.
type mockOTLPReceiver struct {
	mu     sync.Mutex
	spans  []spanInfo
	server *httptest.Server
}

// spanInfo holds basic span information for verification.
type spanInfo struct {
	Name       string
	Attributes map[string]string
}

func newMockOTLPReceiver(t *testing.T) *mockOTLPReceiver {
	t.Helper()
	r := &mockOTLPReceiver{
		spans: make([]spanInfo, 0),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", r.handleTraces)

	r.server = httptest.NewServer(mux)
	t.Cleanup(func() { r.server.Close() })

	return r
}

func (r *mockOTLPReceiver) handleTraces(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer req.Body.Close()

	// Parse the protobuf request.
	var traceReq tracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &traceReq); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Extract span info.
	r.mu.Lock()
	for _, rs := range traceReq.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, span := range ss.Spans {
				info := spanInfo{
					Name:       span.Name,
					Attributes: make(map[string]string),
				}
				for _, attr := range span.Attributes {
					if sv := attr.Value.GetStringValue(); sv != "" {
						info.Attributes[attr.Key] = sv
					}
				}
				r.spans = append(r.spans, info)
			}
		}
	}
	r.mu.Unlock()

	// Return success response.
	resp := &tracepb.ExportTraceServiceResponse{}
	respBytes, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(respBytes)
}

func (r *mockOTLPReceiver) URL() string {
	return r.server.URL
}

func (r *mockOTLPReceiver) Spans() []spanInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]spanInfo, len(r.spans))
	copy(result, r.spans)
	return result
}

func (r *mockOTLPReceiver) WaitForSpans(t *testing.T, minCount int, timeout time.Duration) []spanInfo {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		spans := r.Spans()
		if len(spans) >= minCount {
			return spans
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Timed out waiting for %d spans, got %d", minCount, len(r.Spans()))
	return nil
}

// TestOTLPPluginRegistered verifies the otlp hook is registered in the distro.
func TestOTLPPluginRegistered(t *testing.T) {
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

	require.Contains(t, output, "otlp", "Expected otlp hook to be registered")
	require.Contains(t, output, "Registered plugin hooks", "Expected hook list header")
}

// TestOTLPTracesExported verifies that traces are exported to the OTLP endpoint.
// Uses a mock LLM server to simulate a simple conversation and verifies that
// message spans are exported to the mock OTLP receiver.
func TestOTLPTracesExported(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	// Start mock OTLP receiver.
	otlpReceiver := newMockOTLPReceiver(t)

	// Start mock LLM server with a simple text response.
	llmServer := mockllm.NewServer()
	llmServer.Default(mockllm.TextResponse("Hello! I can help you with that."))
	llmURL := llmServer.Start(t)

	// Create config with both mock LLM and OTLP settings.
	config := map[string]any{
		"options": map[string]any{
			"plugins": map[string]any{
				"otlp": map[string]any{
					"endpoint": otlpReceiver.URL(),
					"insecure": true,
				},
			},
		},
	}
	tmpDir := mockllm.SetupTestEnvWithConfig(t, llmURL, config)

	// Start crush - the config is already written by SetupTestEnvWithConfig.
	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	// Wait for UI to be ready.
	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	// Send a message.
	term.SendText("hello\r")

	// Wait for the assistant to respond.
	require.True(t, testutil.WaitForText(t, term, "Hello", 10*time.Second),
		"Expected assistant response")

	// Wait for spans to be exported (message spans are exported immediately).
	spans := otlpReceiver.WaitForSpans(t, 2, 5*time.Second)

	// Verify span types.
	spanNames := make(map[string]bool)
	for _, s := range spans {
		spanNames[s.Name] = true
	}

	// Message spans are exported immediately during the conversation.
	require.True(t, spanNames["crush.message.user"], "Expected user message span")
	require.True(t, spanNames["crush.message.assistant"], "Expected assistant message span")

	// Verify user message span has expected attributes.
	var userSpan *spanInfo
	for i := range spans {
		if spans[i].Name == "crush.message.user" {
			userSpan = &spans[i]
			break
		}
	}
	require.NotNil(t, userSpan)
	require.Equal(t, "user", userSpan.Attributes["message.role"])
	require.Contains(t, userSpan.Attributes["message.content"], "hello")
	require.NotEmpty(t, userSpan.Attributes["session.id"])
}
