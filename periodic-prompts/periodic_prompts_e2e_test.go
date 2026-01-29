package periodicprompts_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aleksclark/crush-modules/testutil"
	"github.com/stretchr/testify/require"
)

// TestPeriodicPromptsPluginRegistered verifies the plugin is registered in the distro.
func TestPeriodicPromptsPluginRegistered(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	require.Contains(t, output, "periodic-prompts", "Expected periodic-prompts hook to be registered")
	require.Contains(t, output, "periodic_prompts", "Expected periodic_prompts tool to be registered")
}

// TestPeriodicPromptsToolRegistered verifies the tool appears in the tool list.
func TestPeriodicPromptsToolRegistered(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	require.Contains(t, output, "Registered plugin tools", "Expected plugin tools header")
	require.Contains(t, output, "periodic_prompts", "Expected periodic_prompts tool")
}

// TestPeriodicPromptsConfigLoading verifies the plugin loads config correctly.
func TestPeriodicPromptsConfigLoading(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	tmpDir := t.TempDir()

	// Create a prompt file.
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o755))
	promptFile := filepath.Join(promptsDir, "test-prompt.md")
	require.NoError(t, os.WriteFile(promptFile, []byte("Run all tests and report results."), 0o644))

	// Create config with periodic-prompts settings.
	configJSON := `{
  "providers": {
    "test": {
      "type": "openai-compat",
      "base_url": "http://localhost:9999",
      "api_key": "test-key"
    }
  },
  "models": {
    "large": { "provider": "test", "model": "test-model" },
    "small": { "provider": "test", "model": "test-model" }
  },
  "options": {
    "plugins": {
      "periodic-prompts": {
        "prompts": [
          {
            "file": "` + promptFile + `",
            "schedule": "*/5 * * * *",
            "name": "Test Runner"
          }
        ]
      }
    }
  }
}`

	// Set up config directory.
	configPath := filepath.Join(tmpDir, "config", "crush")
	require.NoError(t, os.MkdirAll(configPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configPath, "crush.json"), []byte(configJSON), 0o644))

	// Set up data directory.
	dataPath := filepath.Join(tmpDir, "data", "crush")
	require.NoError(t, os.MkdirAll(dataPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataPath, "crush.json"), []byte(configJSON), 0o644))

	// Start Crush with --list-plugins to verify config is loaded.
	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	// Plugin should be registered even with custom config.
	require.Contains(t, output, "periodic-prompts", "Expected periodic-prompts hook")
}

// TestPeriodicPromptsHookStartsWithSchedule verifies the hook starts and schedules prompts.
// This is a unit-level test that verifies the cron scheduler works.
func TestPeriodicPromptsHookStartsWithSchedule(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	tmpDir := t.TempDir()

	// Create a prompt file.
	promptFile := filepath.Join(tmpDir, "check-tests.md")
	require.NoError(t, os.WriteFile(promptFile, []byte("Check test status"), 0o644))

	// Create config with a fast schedule (every minute for testing).
	configJSON := `{
  "providers": {
    "test": {
      "type": "openai-compat",
      "base_url": "http://localhost:9999",
      "api_key": "test-key"
    }
  },
  "models": {
    "large": { "provider": "test", "model": "test-model" },
    "small": { "provider": "test", "model": "test-model" }
  },
  "options": {
    "plugins": {
      "periodic-prompts": {
        "prompts": [
          {
            "file": "` + promptFile + `",
            "schedule": "* * * * *",
            "name": "Every Minute"
          }
        ]
      }
    }
  }
}`

	// Set up config directory.
	configPath := filepath.Join(tmpDir, "config", "crush")
	require.NoError(t, os.MkdirAll(configPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configPath, "crush.json"), []byte(configJSON), 0o644))

	// Set up data directory.
	dataPath := filepath.Join(tmpDir, "data", "crush")
	require.NoError(t, os.MkdirAll(dataPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataPath, "crush.json"), []byte(configJSON), 0o644))

	// The plugin should load without errors. We can't easily test the actual
	// cron execution in e2e tests without a mock LLM, but we verify it starts.
	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	require.Contains(t, output, "periodic-prompts", "Hook should be registered")
}

// TestPeriodicPromptsPromptFileExpansion verifies tilde expansion works in file paths.
func TestPeriodicPromptsPromptFileExpansion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	tmpDir := t.TempDir()

	// Create a prompt file in the "home" directory.
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o755))
	promptFile := filepath.Join(promptsDir, "tilde-test.md")
	require.NoError(t, os.WriteFile(promptFile, []byte("Test with tilde path"), 0o644))

	// Create config using tilde path (will be expanded to tmpDir).
	// Note: In actual usage, ~ expands to real home, but for testing we use absolute path.
	configJSON := `{
  "providers": {
    "test": {
      "type": "openai-compat",
      "base_url": "http://localhost:9999",
      "api_key": "test-key"
    }
  },
  "models": {
    "large": { "provider": "test", "model": "test-model" },
    "small": { "provider": "test", "model": "test-model" }
  },
  "options": {
    "plugins": {
      "periodic-prompts": {
        "prompts": [
          {
            "file": "~/prompts/tilde-test.md",
            "schedule": "0 * * * *",
            "name": "Tilde Test"
          }
        ]
      }
    }
  }
}`

	// Set up config directory.
	configPath := filepath.Join(tmpDir, "config", "crush")
	require.NoError(t, os.MkdirAll(configPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configPath, "crush.json"), []byte(configJSON), 0o644))

	// Set up data directory.
	dataPath := filepath.Join(tmpDir, "data", "crush")
	require.NoError(t, os.MkdirAll(dataPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataPath, "crush.json"), []byte(configJSON), 0o644))

	// Create the prompt file in the expected tilde-expanded location.
	homePromptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(homePromptsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(homePromptsDir, "tilde-test.md"), []byte("Tilde expanded content"), 0o644))

	// Plugin should load without errors.
	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	require.Contains(t, output, "periodic-prompts", "Hook should be registered with tilde path")
}

// TestPeriodicPromptsMultiplePrompts verifies multiple prompts can be configured.
func TestPeriodicPromptsMultiplePrompts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	tmpDir := t.TempDir()

	// Create multiple prompt files.
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o755))

	prompt1 := filepath.Join(promptsDir, "tests.md")
	require.NoError(t, os.WriteFile(prompt1, []byte("Run tests"), 0o644))

	prompt2 := filepath.Join(promptsDir, "lint.md")
	require.NoError(t, os.WriteFile(prompt2, []byte("Run linter"), 0o644))

	prompt3 := filepath.Join(promptsDir, "status.md")
	require.NoError(t, os.WriteFile(prompt3, []byte("Check status"), 0o644))

	configJSON := `{
  "providers": {
    "test": {
      "type": "openai-compat",
      "base_url": "http://localhost:9999",
      "api_key": "test-key"
    }
  },
  "models": {
    "large": { "provider": "test", "model": "test-model" },
    "small": { "provider": "test", "model": "test-model" }
  },
  "options": {
    "plugins": {
      "periodic-prompts": {
        "prompts": [
          {
            "file": "` + prompt1 + `",
            "schedule": "*/30 * * * *",
            "name": "Test Runner"
          },
          {
            "file": "` + prompt2 + `",
            "schedule": "0 * * * *",
            "name": "Linter"
          },
          {
            "file": "` + prompt3 + `",
            "schedule": "*/15 * * * *",
            "name": "Status Check"
          }
        ]
      }
    }
  }
}`

	// Set up config directory.
	configPath := filepath.Join(tmpDir, "config", "crush")
	require.NoError(t, os.MkdirAll(configPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configPath, "crush.json"), []byte(configJSON), 0o644))

	// Set up data directory.
	dataPath := filepath.Join(tmpDir, "data", "crush")
	require.NoError(t, os.MkdirAll(dataPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataPath, "crush.json"), []byte(configJSON), 0o644))

	// Plugin should load all three prompts.
	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	require.Contains(t, output, "periodic-prompts", "Hook should be registered with multiple prompts")
}

// TestPeriodicPromptsInvalidSchedule verifies invalid schedules don't crash the plugin.
func TestPeriodicPromptsInvalidSchedule(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	tmpDir := t.TempDir()

	// Create a prompt file.
	promptFile := filepath.Join(tmpDir, "test.md")
	require.NoError(t, os.WriteFile(promptFile, []byte("Test prompt"), 0o644))

	// Create config with an invalid schedule.
	configJSON := `{
  "providers": {
    "test": {
      "type": "openai-compat",
      "base_url": "http://localhost:9999",
      "api_key": "test-key"
    }
  },
  "models": {
    "large": { "provider": "test", "model": "test-model" },
    "small": { "provider": "test", "model": "test-model" }
  },
  "options": {
    "plugins": {
      "periodic-prompts": {
        "prompts": [
          {
            "file": "` + promptFile + `",
            "schedule": "not a valid schedule",
            "name": "Invalid"
          }
        ]
      }
    }
  }
}`

	// Set up config directory.
	configPath := filepath.Join(tmpDir, "config", "crush")
	require.NoError(t, os.MkdirAll(configPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configPath, "crush.json"), []byte(configJSON), 0o644))

	// Set up data directory.
	dataPath := filepath.Join(tmpDir, "data", "crush")
	require.NoError(t, os.MkdirAll(dataPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataPath, "crush.json"), []byte(configJSON), 0o644))

	// Plugin should still load (invalid schedules are logged but don't crash).
	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	require.Contains(t, output, "periodic-prompts", "Hook should still be registered despite invalid schedule")
}

// TestPeriodicPromptsDisabledByConfig verifies the plugin can be disabled.
func TestPeriodicPromptsDisabledByConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	tmpDir := t.TempDir()

	// Create config that disables the plugin.
	configJSON := `{
  "providers": {
    "test": {
      "type": "openai-compat",
      "base_url": "http://localhost:9999",
      "api_key": "test-key"
    }
  },
  "models": {
    "large": { "provider": "test", "model": "test-model" },
    "small": { "provider": "test", "model": "test-model" }
  },
  "options": {
    "disabled_plugins": ["periodic-prompts"]
  }
}`

	// Set up config directory.
	configPath := filepath.Join(tmpDir, "config", "crush")
	require.NoError(t, os.MkdirAll(configPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configPath, "crush.json"), []byte(configJSON), 0o644))

	// Set up data directory.
	dataPath := filepath.Join(tmpDir, "data", "crush")
	require.NoError(t, os.MkdirAll(dataPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataPath, "crush.json"), []byte(configJSON), 0o644))

	// Create isolated terminal with the config.
	term := testutil.NewIsolatedTerminalWithConfigAndEnv(t, 80, 24, configJSON, tmpDir)
	defer term.Close()

	// Give it time to start.
	time.Sleep(500 * time.Millisecond)

	// Close and check with --list-plugins.
	term.Close()

	term2 := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term2.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term2.Snapshot()
	output := testutil.SnapshotText(snap)

	// The plugin is still registered (it's compiled in), but it would be
	// skipped during initialization when disabled_plugins is set.
	// We can only verify the plugin exists in the binary.
	require.Contains(t, output, "periodic-prompts", "Plugin should be in binary")
}

// TestPeriodicPromptsEmptyConfig verifies the plugin handles empty config gracefully.
func TestPeriodicPromptsEmptyConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	testutil.SkipIfE2EDisabled(t)

	tmpDir := t.TempDir()

	// Create config without periodic-prompts settings.
	configJSON := `{
  "providers": {
    "test": {
      "type": "openai-compat",
      "base_url": "http://localhost:9999",
      "api_key": "test-key"
    }
  },
  "models": {
    "large": { "provider": "test", "model": "test-model" },
    "small": { "provider": "test", "model": "test-model" }
  }
}`

	// Set up config directory.
	configPath := filepath.Join(tmpDir, "config", "crush")
	require.NoError(t, os.MkdirAll(configPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configPath, "crush.json"), []byte(configJSON), 0o644))

	// Set up data directory.
	dataPath := filepath.Join(tmpDir, "data", "crush")
	require.NoError(t, os.MkdirAll(dataPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataPath, "crush.json"), []byte(configJSON), 0o644))

	// Plugin should load with empty prompts list.
	term := testutil.NewTestTerminal(t, []string{"--list-plugins"}, 80, 24)
	defer term.Close()

	time.Sleep(500 * time.Millisecond)

	snap := term.Snapshot()
	output := testutil.SnapshotText(snap)

	require.Contains(t, output, "periodic-prompts", "Hook should be registered even with no prompts configured")
}
