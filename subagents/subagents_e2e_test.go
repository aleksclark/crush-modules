package subagents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aleksclark/crush-modules/testutil"
	"github.com/aleksclark/crush-modules/testutil/mockllm"
	"github.com/charmbracelet/x/vttest"
	"github.com/stretchr/testify/require"
)

// TestSubAgentPluginRegistered verifies the subagent tool is registered in the distro.
func TestSubAgentPluginRegistered(t *testing.T) {
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

	require.Contains(t, output, "subagent", "Expected subagent tool to be registered")
	require.Contains(t, output, "Registered plugin tools", "Expected tool list header")
}

// TestSubAgentToolInvocation verifies the subagent tool can be invoked.
// Uses a mock LLM server to simulate a conversation where the LLM invokes the subagent tool.
func TestSubAgentToolInvocation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	// Start mock LLM server.
	llmServer := mockllm.NewServer()

	// First response: LLM invokes the subagent tool.
	llmServer.OnMessage("test subagent", mockllm.ToolCallResponse("subagent", map[string]any{
		"agent":  "test-agent",
		"prompt": "Say hello",
	}))

	// After tool result: LLM provides final response.
	llmServer.OnToolResult("subagent", mockllm.TextResponse("The sub-agent responded with a greeting."))

	llmURL := llmServer.Start(t)

	// Create config with mock LLM settings.
	config := map[string]any{
		"options": map[string]any{
			"plugins": map[string]any{
				"subagent": map[string]any{
					"dirs": []string{".crush/agents"},
				},
			},
		},
	}
	tmpDir := mockllm.SetupTestEnvWithConfig(t, llmURL, config)

	// Create the agents directory and a test agent file.
	agentsDir := filepath.Join(tmpDir, ".crush", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("Failed to create agents dir: %v", err)
	}

	agentFile := filepath.Join(agentsDir, "test-agent.md")
	agentContent := `---
name: test-agent
description: A test sub-agent for E2E testing.
model: inherit
---
You are a test sub-agent. When asked to say hello, respond with "Hello from test-agent!"
`
	if err := os.WriteFile(agentFile, []byte(agentContent), 0o644); err != nil {
		t.Fatalf("Failed to write agent file: %v", err)
	}

	// Start crush in the tmpDir.
	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	// Wait for UI to be ready.
	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	// Send a message that will trigger the subagent tool.
	term.SendText("test subagent\r")

	// Wait for the assistant to respond.
	require.True(t, testutil.WaitForText(t, term, "sub-agent", 15*time.Second),
		"Expected assistant response mentioning sub-agent")
}

// TestSubAgentNotFound verifies proper error handling when agent doesn't exist.
func TestSubAgentNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	// Start mock LLM server.
	llmServer := mockllm.NewServer()

	// LLM tries to invoke a non-existent agent.
	llmServer.OnMessage("invoke missing", mockllm.ToolCallResponse("subagent", map[string]any{
		"agent":  "nonexistent-agent",
		"prompt": "Do something",
	}))

	// After tool result with error: LLM responds appropriately.
	// The tool returns an error, so this handler will be called with the error result.
	llmServer.OnToolResult("subagent", mockllm.TextResponse("I tried to call a sub-agent but it was not available."))

	llmURL := llmServer.Start(t)

	// Create config - no agents configured.
	config := map[string]any{
		"options": map[string]any{
			"plugins": map[string]any{
				"subagent": map[string]any{
					"dirs": []string{".crush/agents"},
				},
			},
		},
	}
	tmpDir := mockllm.SetupTestEnvWithConfig(t, llmURL, config)

	// Create empty agents directory (no agents).
	agentsDir := filepath.Join(tmpDir, ".crush", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("Failed to create agents dir: %v", err)
	}

	// Start crush.
	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	// Wait for UI to be ready.
	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	// Send a message that will trigger the subagent tool.
	term.SendText("invoke missing\r")

	// Wait for the final response from the mock LLM.
	// The mock server responds with "I tried to call a sub-agent but it was not available."
	found := testutil.WaitForCondition(t, term, func(snap vttest.Snapshot) bool {
		text := testutil.SnapshotText(snap)
		ltext := strings.ToLower(text)
		// Check for either:
		// - The mock LLM's final response about the sub-agent
		// - Or the tool error message about "sub-agent not found"
		return strings.Contains(ltext, "sub-agent") ||
			strings.Contains(ltext, "not available") ||
			strings.Contains(ltext, "subagent")
	}, 15*time.Second)
	require.True(t, found, "Expected response about sub-agent")
}
