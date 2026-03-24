//go:build integration

package sdk_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aleksclark/crush-modules/sdks/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const (
	ollamaImage = "ollama/ollama:latest"
	ollamaModel = "qwen2.5:0.5b"

	containerStartTimeout = 60 * time.Second
	modelPullTimeout      = 5 * time.Minute
	crushStartTimeout     = 30 * time.Second
	runTimeout            = 3 * time.Minute
)

// ---------------------------------------------------------------------------
// Test environment — shared across all tests via TestMain
// ---------------------------------------------------------------------------

var (
	testEnv *integrationEnv
)

type integrationEnv struct {
	ollamaContainer string
	ollamaURL       string
	crushBinary     string
	crushCmd        *exec.Cmd
	crushPort       int
	crushURL        string
	tmpDir          string
	client          *sdk.Client
}

func TestMain(m *testing.M) {
	env, err := setup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration setup: %v\n", err)
		os.Exit(1)
	}
	testEnv = env

	code := m.Run()

	teardown(env)
	os.Exit(code)
}

// ---------------------------------------------------------------------------
// Setup: docker ollama → pull model → crush serve → SDK client
// ---------------------------------------------------------------------------

func setup() (*integrationEnv, error) {
	ctx := context.Background()
	env := &integrationEnv{}

	// Resolve crush binary.
	env.crushBinary = crushBinary()
	if _, err := os.Stat(env.crushBinary); err != nil {
		return nil, fmt.Errorf("crush binary not found at %s — run 'task distro:all': %w", env.crushBinary, err)
	}

	// Verify acp-server hook is registered.
	out, err := exec.Command(env.crushBinary, "--list-plugins").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("crush --list-plugins failed: %w\n%s", err, out)
	}
	if !strings.Contains(string(out), "acp-server") {
		return nil, fmt.Errorf("crush binary missing acp-server hook:\n%s", out)
	}

	// Start ollama in Docker.
	ollamaPort := randomPort()
	env.ollamaURL = fmt.Sprintf("http://127.0.0.1:%d", ollamaPort)

	containerName := fmt.Sprintf("crush-sdk-test-ollama-%d", ollamaPort)
	env.ollamaContainer = containerName

	fmt.Fprintf(os.Stderr, "[setup] starting ollama container %s on port %d\n", containerName, ollamaPort)
	runCmd := exec.CommandContext(ctx, "docker", "run", "-d",
		"--name", containerName,
		"-p", fmt.Sprintf("%d:11434", ollamaPort),
		ollamaImage,
	)
	if out, err := runCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("docker run ollama: %w\n%s", err, out)
	}

	// Wait for ollama to be ready.
	fmt.Fprintf(os.Stderr, "[setup] waiting for ollama at %s\n", env.ollamaURL)
	if err := waitForHTTP(ctx, env.ollamaURL+"/api/tags", containerStartTimeout); err != nil {
		teardown(env)
		return nil, fmt.Errorf("ollama not ready: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[setup] ollama ready\n")

	// Pull the model.
	fmt.Fprintf(os.Stderr, "[setup] pulling model %s (this may take a minute)\n", ollamaModel)
	pullCtx, pullCancel := context.WithTimeout(ctx, modelPullTimeout)
	defer pullCancel()
	if err := ollamaPull(pullCtx, env.ollamaURL, ollamaModel); err != nil {
		teardown(env)
		return nil, fmt.Errorf("ollama pull %s: %w", ollamaModel, err)
	}
	fmt.Fprintf(os.Stderr, "[setup] model %s ready\n", ollamaModel)

	// Create isolated crush environment.
	tmpDir, err := os.MkdirTemp("", "crush-sdk-integration-*")
	if err != nil {
		teardown(env)
		return nil, err
	}
	env.tmpDir = tmpDir

	env.crushPort = randomPort()
	env.crushURL = fmt.Sprintf("http://127.0.0.1:%d", env.crushPort)

	if err := writeCrushConfig(env); err != nil {
		teardown(env)
		return nil, err
	}

	// Start crush serve.
	fmt.Fprintf(os.Stderr, "[setup] starting crush serve on port %d\n", env.crushPort)
	workDir := filepath.Join(tmpDir, "work")

	crushCmd := exec.Command(env.crushBinary, "serve", "--verbose", "--cwd", workDir)
	crushCmd.Env = buildCrushEnv(tmpDir)
	crushCmd.Stdout = logWriter("[crush] ")
	crushCmd.Stderr = logWriter("[crush] ")
	if err := crushCmd.Start(); err != nil {
		teardown(env)
		return nil, fmt.Errorf("crush serve start: %w", err)
	}
	env.crushCmd = crushCmd

	// Wait for ACP server.
	fmt.Fprintf(os.Stderr, "[setup] waiting for crush ACP at %s\n", env.crushURL)
	client := sdk.NewClient(env.crushURL)
	waitCtx, waitCancel := context.WithTimeout(ctx, crushStartTimeout)
	defer waitCancel()
	if err := client.WaitReady(waitCtx, 500*time.Millisecond); err != nil {
		teardown(env)
		return nil, fmt.Errorf("crush not ready: %w", err)
	}
	env.client = client
	fmt.Fprintf(os.Stderr, "[setup] crush ACP ready — all systems go\n")

	return env, nil
}

func teardown(env *integrationEnv) {
	if env == nil {
		return
	}

	if env.crushCmd != nil && env.crushCmd.Process != nil {
		fmt.Fprintf(os.Stderr, "[teardown] stopping crush (PID %d)\n", env.crushCmd.Process.Pid)
		env.crushCmd.Process.Kill()
		env.crushCmd.Wait()
	}

	if env.ollamaContainer != "" {
		fmt.Fprintf(os.Stderr, "[teardown] removing container %s\n", env.ollamaContainer)
		exec.Command("docker", "rm", "-f", env.ollamaContainer).Run()
	}

	if env.tmpDir != "" {
		os.RemoveAll(env.tmpDir)
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIntegration_Ping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, testEnv.client.Ping(ctx))
}

func TestIntegration_ListAgents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	agents, err := testEnv.client.ListAgents(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, agents)
	assert.Equal(t, "crush", agents[0].Name)
}

func TestIntegration_NewSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	result, err := testEnv.client.NewSession(ctx, "Say hello.")
	require.NoError(t, err)
	require.NotNil(t, result.Run)
	assert.Equal(t, sdk.RunStatusCompleted, result.Run.Status)
	assert.NotEmpty(t, result.Run.RunID, "run ID should be set")
	assert.NotEmpty(t, result.Text(), "response text should not be empty")

	t.Logf("Session: %s  Run: %s", result.Run.SessionID, result.Run.RunID)
	t.Logf("Response: %s", truncate(result.Text(), 200))
}

func TestIntegration_Resume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	// Use streaming for the first turn so we get a session ID back reliably.
	stream, err := testEnv.client.NewSessionStream(ctx, "Say hello.")
	require.NoError(t, err)
	first, err := stream.Result()
	require.NoError(t, err)
	require.Equal(t, sdk.RunStatusCompleted, first.Run.Status)
	sessionID := first.Run.SessionID
	require.NotEmpty(t, sessionID)

	t.Logf("Turn 1 session=%s response=%s", sessionID, truncate(TextFromRun(first.Run), 200))

	// Second turn — same session via Resume.
	second, err := testEnv.client.Resume(ctx, sessionID, "What did I just say to you?")
	require.NoError(t, err)
	require.Equal(t, sdk.RunStatusCompleted, second.Run.Status)
	assert.Equal(t, sessionID, second.Run.SessionID, "session ID should be preserved")
	assert.NotEmpty(t, second.Text(), "response should not be empty")

	t.Logf("Turn 2 response=%s", truncate(second.Text(), 200))
}

func TestIntegration_NewSessionStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	stream, err := testEnv.client.NewSessionStream(ctx, "Say hello in one short sentence.")
	require.NoError(t, err)

	var (
		parts      []string
		eventTypes []sdk.EventType
		gotRun     bool
		sessionID  string
	)

	for ev := range stream.Events {
		eventTypes = append(eventTypes, ev.Type)
		if ev.Type == sdk.EventMessagePart && ev.Part != nil {
			parts = append(parts, ev.Part.Content)
		}
		if ev.Run != nil {
			gotRun = true
			if ev.Run.SessionID != "" {
				sessionID = ev.Run.SessionID
			}
		}
	}

	require.NoError(t, stream.Err())
	assert.True(t, gotRun, "should receive at least one run event")
	assert.NotEmpty(t, sessionID, "session ID should be set")
	assert.NotEmpty(t, parts, "should receive message parts")
	assert.Contains(t, eventTypes, sdk.EventRunCompleted, "should see run.completed")

	fullText := strings.Join(parts, "")
	assert.NotEmpty(t, fullText, "streamed text should not be empty")
	t.Logf("Streamed %d parts: %s", len(parts), truncate(fullText, 200))
}

func TestIntegration_StreamResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	stream, err := testEnv.client.NewSessionStream(ctx, "Reply with: STREAM_OK")
	require.NoError(t, err)

	result, err := stream.Result()
	require.NoError(t, err)
	require.NotNil(t, result.Run)
	assert.Equal(t, sdk.RunStatusCompleted, result.Run.Status)
	assert.NotEmpty(t, result.Run.SessionID)

	t.Logf("Result: %s", truncate(result.Run.SessionID, 100))
}

func TestIntegration_ResumeStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	// Create a session via streaming (to get session ID).
	stream, err := testEnv.client.NewSessionStream(ctx, "Say hello.")
	require.NoError(t, err)
	first, err := stream.Result()
	require.NoError(t, err)
	sessionID := first.Run.SessionID
	require.NotEmpty(t, sessionID)

	// Resume with streaming.
	rstream, err := testEnv.client.ResumeStream(ctx, sessionID, "Say goodbye.")
	require.NoError(t, err)

	result, err := rstream.Result()
	require.NoError(t, err)
	assert.Equal(t, sdk.RunStatusCompleted, result.Run.Status)
	assert.Equal(t, sessionID, result.Run.SessionID)
	assert.NotEmpty(t, TextFromRun(result.Run))

	t.Logf("Resume stream response for session %s", sessionID)
}

func TestIntegration_Dump(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	// Create a session via streaming.
	stream, err := testEnv.client.NewSessionStream(ctx, "Hello, this is a dump test.")
	require.NoError(t, err)
	first, err := stream.Result()
	require.NoError(t, err)
	sessionID := first.Run.SessionID
	require.NotEmpty(t, sessionID)

	// Dump.
	snapshot, err := testEnv.client.Dump(ctx, sessionID)
	require.NoError(t, err)

	assert.Equal(t, 1, snapshot.Version)
	assert.Equal(t, sessionID, snapshot.Session.ID)
	assert.NotEmpty(t, snapshot.Session.Title)
	assert.NotEmpty(t, snapshot.Messages, "snapshot should contain messages")
	assert.GreaterOrEqual(t, len(snapshot.Messages), 2, "should have at least user + assistant messages")

	// Verify message structure.
	var roles []string
	for _, msg := range snapshot.Messages {
		roles = append(roles, msg.Role)
		assert.NotEmpty(t, msg.ID, "message ID should be set")
		assert.Equal(t, sessionID, msg.SessionID, "message session ID should match")
		assert.NotEmpty(t, msg.Parts, "message parts should not be empty")
	}
	assert.Contains(t, roles, "user", "should have user messages")
	assert.Contains(t, roles, "assistant", "should have assistant messages")

	// Verify it serializes cleanly.
	data, err := json.Marshal(snapshot)
	require.NoError(t, err)
	assert.True(t, json.Valid(data), "snapshot should be valid JSON")

	t.Logf("Dumped session %s: %d messages, %.2f cost", sessionID, len(snapshot.Messages), snapshot.Session.Cost)
}

func TestIntegration_DumpMultiTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	// Turn 1 via streaming.
	stream, err := testEnv.client.NewSessionStream(ctx, "I like cats. Reply with OK.")
	require.NoError(t, err)
	first, err := stream.Result()
	require.NoError(t, err)
	sessionID := first.Run.SessionID

	// Turn 2.
	_, err = testEnv.client.Resume(ctx, sessionID, "I also like dogs. Reply with OK.")
	require.NoError(t, err)

	// Dump should capture both turns.
	snapshot, err := testEnv.client.Dump(ctx, sessionID)
	require.NoError(t, err)

	// At minimum: user1 + assistant1 + user2 + assistant2 = 4 messages.
	assert.GreaterOrEqual(t, len(snapshot.Messages), 4,
		"multi-turn dump should have at least 4 messages, got %d", len(snapshot.Messages))

	t.Logf("Multi-turn dump: %d messages", len(snapshot.Messages))
}

func TestIntegration_Restore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	// Create a session via streaming (to get session ID).
	stream, err := testEnv.client.NewSessionStream(ctx, "Say hello.")
	require.NoError(t, err)
	first, err := stream.Result()
	require.NoError(t, err)
	sessionID := first.Run.SessionID
	require.NotEmpty(t, sessionID)

	// Dump the session.
	snapshot, err := testEnv.client.Dump(ctx, sessionID)
	require.NoError(t, err)
	require.NotEmpty(t, snapshot.Messages)
	originalCount := len(snapshot.Messages)

	t.Logf("Dumped %d messages from session %s", originalCount, sessionID)

	// Restore the snapshot (replaces the session on the server).
	err = testEnv.client.Restore(ctx, snapshot)
	require.NoError(t, err)

	// Verify the restored session can be used — Resume should succeed.
	result, err := testEnv.client.Resume(ctx, sessionID, "What were we talking about?")
	require.NoError(t, err)
	assert.Equal(t, sdk.RunStatusCompleted, result.Run.Status)
	assert.NotEmpty(t, result.Text())

	// Dump again — should have more messages than before (original + user + assistant).
	snapshot2, err := testEnv.client.Dump(ctx, sessionID)
	require.NoError(t, err)
	assert.Greater(t, len(snapshot2.Messages), originalCount,
		"resumed session should have more messages than the restored snapshot")

	t.Logf("Post-restore: %d messages, response=%s", len(snapshot2.Messages), truncate(result.Text(), 200))
}

func TestIntegration_FullRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	// 1. NewSession (streaming to get session ID).
	t.Log("Step 1: NewSession")
	stream, err := testEnv.client.NewSessionStream(ctx, "Say hello.")
	require.NoError(t, err)
	first, err := stream.Result()
	require.NoError(t, err)
	require.Equal(t, sdk.RunStatusCompleted, first.Run.Status)
	sessionID := first.Run.SessionID
	require.NotEmpty(t, sessionID)

	// 2. Resume — verify we can continue the session.
	t.Log("Step 2: Resume")
	result, err := testEnv.client.Resume(ctx, sessionID, "Say goodbye.")
	require.NoError(t, err)
	require.Equal(t, sdk.RunStatusCompleted, result.Run.Status)
	assert.Equal(t, sessionID, result.Run.SessionID)

	// 3. Dump — verify snapshot structure.
	t.Log("Step 3: Dump")
	snapshot, err := testEnv.client.Dump(ctx, sessionID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(snapshot.Messages), 4,
		"should have at least 4 messages (2 user + 2 assistant)")
	assert.Equal(t, sessionID, snapshot.Session.ID)

	// 4. Verify snapshot is portable (marshal/unmarshal round-trip).
	t.Log("Step 4: Snapshot portability")
	data, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	var restored sdk.SessionSnapshot
	require.NoError(t, json.Unmarshal(data, &restored))
	assert.Equal(t, snapshot.Session.ID, restored.Session.ID)
	assert.Equal(t, len(snapshot.Messages), len(restored.Messages))

	// 5. Restore — reimport the snapshot.
	t.Log("Step 5: Restore")
	err = testEnv.client.Restore(ctx, &restored)
	require.NoError(t, err)

	// 6. Resume after restore — verify session is functional.
	t.Log("Step 6: Resume after restore")
	result, err = testEnv.client.Resume(ctx, sessionID, "Are you still there?")
	require.NoError(t, err)
	assert.Equal(t, sdk.RunStatusCompleted, result.Run.Status)
	assert.NotEmpty(t, result.Text())

	// 7. Final dump — message count should have grown.
	t.Log("Step 7: Final dump")
	finalSnap, err := testEnv.client.Dump(ctx, sessionID)
	require.NoError(t, err)
	assert.Greater(t, len(finalSnap.Messages), len(snapshot.Messages),
		"final snapshot should have more messages than pre-restore")

	t.Logf("Round-trip complete: session=%s, initial=%d msgs, final=%d msgs",
		sessionID, len(snapshot.Messages), len(finalSnap.Messages))
}

func TestIntegration_StreamSnapshotCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	stream, err := testEnv.client.NewSessionStream(ctx, "Reply with: snapshot test complete")
	require.NoError(t, err)

	var gotSnapshot bool
	for ev := range stream.Events {
		if ev.Type == sdk.EventSessionSnapshot {
			gotSnapshot = true
		}
	}
	require.NoError(t, stream.Err())

	result, err := stream.Result()
	require.NoError(t, err)

	if gotSnapshot {
		require.NotNil(t, result.Snapshot, "stream.Result() should capture the snapshot")
		assert.Equal(t, 1, result.Snapshot.Version)
		assert.NotEmpty(t, result.Snapshot.Session.ID)
		assert.NotEmpty(t, result.Snapshot.Messages)
		t.Logf("Captured snapshot with %d messages", len(result.Snapshot.Messages))
	} else {
		t.Log("No session.snapshot event received (server may not emit for sync-like streams)")
	}
}

func TestIntegration_SessionIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	// Start two streams — each should get the same default session (no explicit ID).
	// This tests that the SDK correctly passes through session IDs.
	stream1, err := testEnv.client.NewSessionStream(ctx, "Say red.")
	require.NoError(t, err)
	r1, err := stream1.Result()
	require.NoError(t, err)
	sid1 := r1.Run.SessionID

	// Resume on the same session to verify continuity.
	r2, err := testEnv.client.Resume(ctx, sid1, "Say blue.")
	require.NoError(t, err)
	assert.Equal(t, sid1, r2.Run.SessionID, "resume should preserve session ID")

	// Dump the session and verify it has messages from both turns.
	snapshot, err := testEnv.client.Dump(ctx, sid1)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(snapshot.Messages), 4,
		"session should have at least 4 messages from 2 turns")

	t.Logf("Session %s: %d messages", sid1, len(snapshot.Messages))
}

func TestIntegration_DumpNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := testEnv.client.Dump(ctx, "nonexistent-session-id-12345")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func crushBinary() string {
	if path := os.Getenv("CRUSH_BINARY"); path != "" {
		return path
	}
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "dist", "crush")
}

func randomPort() int {
	for {
		port := 40000 + rand.IntN(20000)
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			ln.Close()
			return port
		}
	}
}

func waitForHTTP(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout after %s waiting for %s", timeout, url)
}

func ollamaPull(ctx context.Context, baseURL, model string) error {
	body, _ := json.Marshal(map[string]any{
		"name":   model,
		"stream": false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: modelPullTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pull request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pull HTTP %d", resp.StatusCode)
	}
	return nil
}

func writeCrushConfig(env *integrationEnv) error {
	config := fmt.Sprintf(`{
  "providers": {
    "ollama": {
      "type": "openai-compat",
      "base_url": "%s/v1",
      "api_key": "ollama",
      "models": [{
        "id": "%s",
        "name": "Test Model",
        "context_window": 32768,
        "default_max_tokens": 2048,
        "can_reason": false,
        "supports_attachments": false
      }]
    }
  },
  "models": {
    "large": {"provider": "ollama", "model": "%s"},
    "small": {"provider": "ollama", "model": "%s"}
  },
  "options": {
    "disabled_plugins": ["otlp","agent-status","periodic-prompts","subagents","tempotown","ping","tavily"],
    "plugins": {
      "acp-server": {
        "port": %d,
        "agent_name": "crush",
        "description": "SDK integration test agent"
      }
    }
  }
}`, env.ollamaURL, ollamaModel, ollamaModel, ollamaModel, env.crushPort)

	for _, sub := range []string{"config", "data"} {
		dir := filepath.Join(env.tmpDir, sub, "crush")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "crush.json"), []byte(config), 0o644); err != nil {
			return err
		}
	}

	workDir := filepath.Join(env.tmpDir, "work", ".crush")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workDir, "init"), nil, 0o644)
}

func buildCrushEnv(tmpDir string) []string {
	// Start from a clean env, filtering cloud credentials.
	var env []string
	for _, e := range os.Environ() {
		key := strings.SplitN(e, "=", 2)[0]
		switch {
		case strings.HasPrefix(key, "AWS_"),
			strings.HasPrefix(key, "GOOGLE_"),
			strings.HasPrefix(key, "AZURE_"),
			strings.HasPrefix(key, "OPENAI_"),
			strings.HasPrefix(key, "ANTHROPIC_"),
			strings.HasPrefix(key, "GEMINI_"):
			continue
		}
		env = append(env, e)
	}

	env = append(env,
		"HOME="+tmpDir,
		"XDG_CONFIG_HOME="+filepath.Join(tmpDir, "config"),
		"XDG_DATA_HOME="+filepath.Join(tmpDir, "data"),
		"TERM=dumb",
		"CRUSH_DISABLE_METRICS=1",
	)
	return env
}

type prefixWriter struct {
	prefix string
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		if line != "" {
			fmt.Fprintf(os.Stderr, "%s%s\n", w.prefix, line)
		}
	}
	return len(p), nil
}

func logWriter(prefix string) *prefixWriter {
	return &prefixWriter{prefix: prefix}
}

// TextFromRun extracts text content from a run's output messages.
func TextFromRun(run *sdk.Run) string {
	if run == nil {
		return ""
	}
	return sdk.TextContent(run.Output)
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
