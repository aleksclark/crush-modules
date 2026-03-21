//go:build e2e

package periodicprompts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aleksclark/crush-modules/testutil"
	"github.com/aleksclark/crush-modules/testutil/mockllm"
	"github.com/stretchr/testify/require"
)

// TestPeriodicPromptsPluginRegistered verifies both the hook and tool are registered.
func TestPeriodicPromptsPluginRegistered(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	output := testutil.SnapshotText(term.Snapshot())

	require.Contains(t, output, "periodic-prompts", "Expected periodic-prompts hook to be registered")
	require.Contains(t, output, "periodic_prompts", "Expected periodic_prompts tool to be registered")
	require.Contains(t, output, "Registered plugin tools", "Expected tool list header")
}

// TestPeriodicPromptsToolStatus verifies the LLM can invoke the tool and get status.
func TestPeriodicPromptsToolStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	server := mockllm.NewServer()
	server.OnMessage("status", mockllm.ToolCallResponse("periodic_prompts", map[string]any{"action": "status"}))
	server.OnToolResult("periodic_prompts", mockllm.TextResponse("Periodic prompting is currently disabled."))
	url := server.Start(t)

	tmpDir := mockllm.SetupTestEnv(t, url)
	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	term.SendText("what is the periodic prompts status\r")

	require.True(t, testutil.WaitForText(t, term, "disabled", 15*time.Second),
		"Expected status response containing 'disabled'")

	mockllm.AssertToolWasCalled(t, server, "periodic_prompts")
}

// TestPeriodicPromptsToolEnable verifies the LLM can enable periodic prompting.
func TestPeriodicPromptsToolEnable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	server := mockllm.NewServer()
	server.OnMessage("enable", mockllm.ToolCallResponse("periodic_prompts", map[string]any{"action": "enable"}))
	server.OnToolResult("periodic_prompts", mockllm.TextResponse("Periodic prompting has been enabled."))
	url := server.Start(t)

	tmpDir := mockllm.SetupTestEnv(t, url)
	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	term.SendText("enable periodic prompts\r")

	require.True(t, testutil.WaitForText(t, term, "enabled", 15*time.Second),
		"Expected enable confirmation")

	mockllm.AssertToolWasCalled(t, server, "periodic_prompts")
}

// TestPeriodicPromptsToolDisable verifies the LLM can disable periodic prompting.
func TestPeriodicPromptsToolDisable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	server := mockllm.NewServer()
	server.OnMessage("disable", mockllm.ToolCallResponse("periodic_prompts", map[string]any{"action": "disable"}))
	server.OnToolResult("periodic_prompts", mockllm.TextResponse("Periodic prompting has been disabled."))
	url := server.Start(t)

	tmpDir := mockllm.SetupTestEnv(t, url)
	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	term.SendText("disable periodic prompts\r")

	require.True(t, testutil.WaitForText(t, term, "disabled", 15*time.Second),
		"Expected disable confirmation")

	mockllm.AssertToolWasCalled(t, server, "periodic_prompts")
}

// TestPeriodicPromptsToolList verifies the list action reports configured prompts.
func TestPeriodicPromptsToolList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	tmpDir := t.TempDir()

	promptFile := filepath.Join(tmpDir, "work", "daily.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(promptFile), 0o755))
	require.NoError(t, os.WriteFile(promptFile, []byte("Run daily checks"), 0o644))

	server := mockllm.NewServer()
	server.OnMessage("list", mockllm.ToolCallResponse("periodic_prompts", map[string]any{"action": "list"}))
	server.OnToolResult("periodic_prompts", mockllm.TextResponse("You have 1 configured prompt: Daily Checks."))
	url := server.Start(t)

	config := map[string]any{
		"options": map[string]any{
			"plugins": map[string]any{
				"periodic-prompts": map[string]any{
					"prompts": []map[string]any{
						{
							"file":     promptFile,
							"schedule": "0 9 * * *",
							"name":     "Daily Checks",
						},
					},
				},
			},
		},
	}

	configJSON, err := buildConfig(url, config)
	require.NoError(t, err)
	writeCrushConfig(t, tmpDir, configJSON)

	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	term.SendText("list periodic prompts\r")

	require.True(t, testutil.WaitForText(t, term, "Daily Checks", 15*time.Second),
		"Expected list response mentioning configured prompt name")

	mockllm.AssertToolWasCalled(t, server, "periodic_prompts")
}

// TestPeriodicPromptsEnableArmsScheduledPrompts verifies that enabling periodic prompts
// via the tool arms the scheduler and the hook reports as enabled.
func TestPeriodicPromptsEnableArmsScheduledPrompts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	tmpDir := t.TempDir()
	promptContent := "Run all tests and report the results."
	promptFile := filepath.Join(tmpDir, "work", "prompt.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(promptFile), 0o755))
	require.NoError(t, os.WriteFile(promptFile, []byte(promptContent), 0o644))

	server := mockllm.NewServer()
	server.OnMessage("enable", mockllm.ToolCallResponse("periodic_prompts", map[string]any{"action": "enable"}))
	server.OnToolResult("periodic_prompts", mockllm.TextResponse("Periodic prompting enabled. 1 prompt(s) scheduled."))
	url := server.Start(t)

	config := map[string]any{
		"options": map[string]any{
			"plugins": map[string]any{
				"periodic-prompts": map[string]any{
					"prompts": []map[string]any{
						{
							"file":     promptFile,
							"schedule": "* * * * *",
							"name":     "Test Runner",
						},
					},
				},
			},
		},
	}

	configJSON, err := buildConfig(url, config)
	require.NoError(t, err)
	writeCrushConfig(t, tmpDir, configJSON)

	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	term.SendText("enable periodic prompts\r")

	require.True(t, testutil.WaitForText(t, term, "scheduled", 15*time.Second),
		"Expected tool response confirming scheduled prompts count")

	mockllm.AssertToolWasCalled(t, server, "periodic_prompts")
}

// TestPeriodicPromptsMultiplePromptsConfigured verifies multiple prompts are all
// reported by the list action.
func TestPeriodicPromptsMultiplePromptsConfigured(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	prompt1 := filepath.Join(workDir, "tests.md")
	prompt2 := filepath.Join(workDir, "lint.md")
	prompt3 := filepath.Join(workDir, "status.md")
	require.NoError(t, os.WriteFile(prompt1, []byte("Run tests"), 0o644))
	require.NoError(t, os.WriteFile(prompt2, []byte("Run linter"), 0o644))
	require.NoError(t, os.WriteFile(prompt3, []byte("Check status"), 0o644))

	server := mockllm.NewServer()
	server.OnAny(mockllm.ToolCallResponse("periodic_prompts", map[string]any{"action": "list"}))
	server.OnToolResult("periodic_prompts", mockllm.TextResponse("You have 3 prompts configured: Test Runner, Linter, Status Check."))
	url := server.Start(t)

	config := map[string]any{
		"options": map[string]any{
			"plugins": map[string]any{
				"periodic-prompts": map[string]any{
					"prompts": []map[string]any{
						{"file": prompt1, "schedule": "*/30 * * * *", "name": "Test Runner"},
						{"file": prompt2, "schedule": "0 * * * *", "name": "Linter"},
						{"file": prompt3, "schedule": "*/15 * * * *", "name": "Status Check"},
					},
				},
			},
		},
	}

	configJSON, err := buildConfig(url, config)
	require.NoError(t, err)
	writeCrushConfig(t, tmpDir, configJSON)

	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	term.SendText("list configured prompts\r")

	require.True(t, testutil.WaitForText(t, term, "3 prompts", 15*time.Second),
		"Expected all 3 prompts to be mentioned in response")

	mockllm.AssertToolWasCalled(t, server, "periodic_prompts")
}

// TestPeriodicPromptsInvalidScheduleDoesNotCrash verifies an invalid cron expression
// doesn't crash Crush — the plugin should log the error and remain responsive.
func TestPeriodicPromptsInvalidScheduleDoesNotCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "work", "test.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(promptFile), 0o755))
	require.NoError(t, os.WriteFile(promptFile, []byte("Test prompt"), 0o644))

	server := mockllm.NewServer()
	server.Default(mockllm.TextResponse("I'm alive and well."))
	url := server.Start(t)

	config := map[string]any{
		"options": map[string]any{
			"plugins": map[string]any{
				"periodic-prompts": map[string]any{
					"prompts": []map[string]any{
						{
							"file":     promptFile,
							"schedule": "not a valid cron expression",
							"name":     "Bad Schedule",
						},
					},
				},
			},
		},
	}

	configJSON, err := buildConfig(url, config)
	require.NoError(t, err)
	writeCrushConfig(t, tmpDir, configJSON)

	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second),
		"Crush should start normally even with an invalid cron schedule")

	term.SendText("hello\r")
	require.True(t, testutil.WaitForText(t, term, "alive", 15*time.Second),
		"Crush should be responsive after loading plugin with bad schedule")
}

// TestPeriodicPromptsEmptyConfigStillWorks verifies the plugin starts fine
// when no prompts are configured, and reports 0 configured prompts.
func TestPeriodicPromptsEmptyConfigStillWorks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	server := mockllm.NewServer()
	server.OnMessage("status", mockllm.ToolCallResponse("periodic_prompts", map[string]any{"action": "status"}))
	server.OnToolResult("periodic_prompts", mockllm.TextResponse("Periodic prompting is disabled. Configured prompts: 0"))
	url := server.Start(t)

	tmpDir := mockllm.SetupTestEnv(t, url)
	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	term.SendText("check periodic prompts status\r")
	require.True(t, testutil.WaitForText(t, term, "Configured prompts: 0", 15*time.Second),
		"Expected 0 prompts configured when no config is provided")

	mockllm.AssertToolWasCalled(t, server, "periodic_prompts")
}

// TestPeriodicPromptsDisabledPlugin verifies Crush works normally with plugin disabled
// and that the periodic_prompts tool is never called.
func TestPeriodicPromptsDisabledPlugin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	server := mockllm.NewServer()
	server.Default(mockllm.TextResponse("Hello! I'm ready to help."))
	url := server.Start(t)

	config := map[string]any{
		"options": map[string]any{
			"disabled_plugins": []string{"periodic-prompts", "periodic_prompts"},
		},
	}
	tmpDir := mockllm.SetupTestEnvWithConfig(t, url, config)

	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	term.SendText("hello\r")
	require.True(t, testutil.WaitForText(t, term, "ready", 15*time.Second),
		"Crush should respond normally without periodic-prompts")

	mockllm.AssertToolWasNotCalled(t, server, "periodic_prompts")
}

// TestPeriodicPromptsEnableDisableCycle drives a full enable→status→disable→status cycle
// through the LLM tool interface using a scripted conversation sequence.
func TestPeriodicPromptsEnableDisableCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	server := mockllm.NewServer()
	mockllm.NewConversation(server).
		ThenTool("periodic_prompts", map[string]any{"action": "enable"}).
		ThenText("Periodic prompting is now enabled.").
		ThenTool("periodic_prompts", map[string]any{"action": "status"}).
		ThenText("Periodic prompting is enabled. Configured prompts: 0").
		ThenTool("periodic_prompts", map[string]any{"action": "disable"}).
		ThenText("Periodic prompting is now disabled.").
		ThenTool("periodic_prompts", map[string]any{"action": "status"}).
		ThenText("Periodic prompting is disabled. Configured prompts: 0").
		Apply()

	url := server.Start(t)
	tmpDir := mockllm.SetupTestEnv(t, url)
	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 100, 30, "", tmpDir)
	defer term.Close()

	require.True(t, testutil.WaitForText(t, term, ">", 5*time.Second), "UI should be ready")

	term.SendText("enable periodic prompts then check status then disable and check status again\r")

	require.True(t, testutil.WaitForText(t, term, "disabled", 20*time.Second),
		"Expected final status to show disabled after full enable/disable cycle")

	mockllm.AssertToolWasCalled(t, server, "periodic_prompts")
}

// ── helpers ──────────────────────────────────────────────────────────────────

// buildConfig merges mock LLM provider settings with the provided plugin config
// and returns JSON suitable for crush.json.
func buildConfig(serverURL string, extra map[string]any) (string, error) {
	base := map[string]any{
		"providers": map[string]any{
			"mock": map[string]any{
				"type":     "openai-compat",
				"base_url": serverURL,
				"api_key":  "mock-key",
				"models": []map[string]any{
					{
						"id":                   "mock-model",
						"name":                 "Mock Model",
						"context_window":       128000,
						"default_max_tokens":   4096,
						"can_reason":           false,
						"supports_attachments": false,
					},
				},
			},
		},
		"models": map[string]any{
			"large": map[string]any{"provider": "mock", "model": "mock-model"},
			"small": map[string]any{"provider": "mock", "model": "mock-model"},
		},
	}
	for k, v := range extra {
		base[k] = v
	}
	b, err := json.MarshalIndent(base, "", "  ")
	return string(b), err
}

// writeCrushConfig writes config JSON to both config and data dirs inside tmpDir.
func writeCrushConfig(t *testing.T, tmpDir, configJSON string) {
	t.Helper()
	for _, sub := range []string{"config/crush", "data/crush"} {
		dir := filepath.Join(tmpDir, sub)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "crush.json"), []byte(configJSON), 0o644))
	}
}
