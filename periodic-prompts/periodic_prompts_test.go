package periodicprompts

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/plugin"
	"github.com/stretchr/testify/require"
)

func TestNewHook(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Prompts: []PromptConfig{
			{
				File:     "~/prompts/test.md",
				Schedule: "*/5 * * * *",
				Name:     "Test Prompt",
			},
		},
	}

	hook, err := NewHook(nil, cfg)
	require.NoError(t, err)
	require.NotNil(t, hook)
	require.Equal(t, HookName, hook.Name())
	require.False(t, hook.IsEnabled())
}

func TestHookEnableDisable(t *testing.T) {
	t.Parallel()

	hook, err := NewHook(nil, Config{})
	require.NoError(t, err)

	require.False(t, hook.IsEnabled())

	hook.SetEnabled(true)
	require.True(t, hook.IsEnabled())

	hook.SetEnabled(false)
	require.False(t, hook.IsEnabled())
}

func TestReadPromptFile(t *testing.T) {
	t.Parallel()

	hook, err := NewHook(nil, Config{})
	require.NoError(t, err)

	// Create temp file.
	tmpDir := t.TempDir()
	promptPath := filepath.Join(tmpDir, "test-prompt.md")
	content := "Run all tests and report any failures."
	require.NoError(t, os.WriteFile(promptPath, []byte(content), 0o644))

	// Test reading.
	result, err := hook.readPromptFile(promptPath)
	require.NoError(t, err)
	require.Equal(t, content, result)
}

func TestReadPromptFileTilde(t *testing.T) {
	t.Parallel()

	hook, err := NewHook(nil, Config{})
	require.NoError(t, err)

	// Test that ~ expansion doesn't crash (file won't exist).
	_, err = hook.readPromptFile("~/nonexistent/prompt.md")
	require.Error(t, err)
}

func TestGetPrompts(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Prompts: []PromptConfig{
			{File: "a.md", Schedule: "* * * * *"},
			{File: "b.md", Schedule: "0 * * * *"},
		},
	}

	hook, err := NewHook(nil, cfg)
	require.NoError(t, err)

	prompts := hook.GetPrompts()
	require.Len(t, prompts, 2)
	require.Equal(t, "a.md", prompts[0].File)
	require.Equal(t, "b.md", prompts[1].File)
}

func TestToolMetadata(t *testing.T) {
	t.Parallel()

	tool := NewTool(nil)
	info := tool.Info()

	require.Equal(t, ToolName, info.Name)
	require.Contains(t, info.Description, "periodic prompts")
}

func TestToolActions(t *testing.T) {
	// Not parallel - this test modifies global singleton state.

	// Create a hook instance for the tool to use.
	cfg := Config{
		Prompts: []PromptConfig{
			{File: "test.md", Schedule: "*/5 * * * *", Name: "Test"},
		},
	}
	_, err := NewHook(nil, cfg)
	require.NoError(t, err)

	tool := NewTool(nil)

	tests := []struct {
		name     string
		action   string
		contains string
	}{
		{
			name:     "status",
			action:   "status",
			contains: "disabled",
		},
		{
			name:     "enable",
			action:   "enable",
			contains: "enabled",
		},
		{
			name:     "disable",
			action:   "disable",
			contains: "disabled",
		},
		{
			name:     "list",
			action:   "list",
			contains: "Test",
		},
		{
			name:     "unknown",
			action:   "invalid",
			contains: "unknown action",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			call := fantasy.ToolCall{
				ID:    "test-call",
				Name:  ToolName,
				Input: `{"action": "` + tc.action + `"}`,
			}

			resp, err := tool.Run(context.Background(), call)
			require.NoError(t, err)
			require.Contains(t, resp.Content, tc.contains)
		})
	}
}

func TestCronScheduleParsing(t *testing.T) {
	// Not parallel - modifies global singleton.

	// Test that cron schedules are parsed correctly by starting the hook.
	cfg := Config{
		Prompts: []PromptConfig{
			{File: "test.md", Schedule: "*/5 * * * *"},  // Every 5 minutes.
			{File: "test2.md", Schedule: "0 */2 * * *"}, // Every 2 hours.
		},
	}

	hook, err := NewHook(nil, cfg)
	require.NoError(t, err)

	// Start in a goroutine with a short context.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start should not return an error for valid schedules.
	go func() {
		_ = hook.Start(ctx)
	}()

	// Wait for context to be done.
	<-ctx.Done()

	// Stop the cron.
	require.NoError(t, hook.Stop())
}

func TestInvalidCronSchedule(t *testing.T) {
	// Not parallel - modifies global singleton.

	// Test that invalid schedules are logged but don't crash.
	cfg := Config{
		Prompts: []PromptConfig{
			{File: "test.md", Schedule: "invalid schedule"},
		},
	}

	hook, err := NewHook(nil, cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go func() {
		_ = hook.Start(ctx)
	}()

	<-ctx.Done()
	require.NoError(t, hook.Stop())
}

func TestDialogCreation(t *testing.T) {
	// Not parallel - modifies global singleton.

	// Create a hook instance first.
	cfg := Config{
		Prompts: []PromptConfig{
			{File: "test1.md", Schedule: "*/5 * * * *", Name: "Test 1"},
			{File: "test2.md", Schedule: "0 * * * *", Name: "Test 2"},
		},
	}
	_, err := NewHook(nil, cfg)
	require.NoError(t, err)

	// Create the dialog.
	dialog, err := NewDialog(nil)
	require.NoError(t, err)
	require.NotNil(t, dialog)

	require.Equal(t, DialogID, dialog.ID())
	require.Equal(t, "Periodic Prompts", dialog.Title())

	// Check initial view.
	view := dialog.View()
	require.Contains(t, view, "Enable All")
	require.Contains(t, view, "Test 1")
	require.Contains(t, view, "Test 2")
	require.Contains(t, view, "*/5 * * * *")
}

func TestDialogNavigation(t *testing.T) {
	// Not parallel - modifies global singleton.

	cfg := Config{
		Prompts: []PromptConfig{
			{File: "a.md", Schedule: "* * * * *", Name: "A"},
			{File: "b.md", Schedule: "* * * * *", Name: "B"},
		},
	}
	_, err := NewHook(nil, cfg)
	require.NoError(t, err)

	dialog, err := NewDialog(nil)
	require.NoError(t, err)

	d := dialog.(*Dialog)

	// Initial cursor at 0 (all toggle).
	require.Equal(t, 0, d.cursor)

	// Move down.
	done, _, err := dialog.Update(plugin.KeyEvent{Key: "down"})
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, 1, d.cursor)

	// Move down again.
	done, _, err = dialog.Update(plugin.KeyEvent{Key: "down"})
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, 2, d.cursor)

	// Move up.
	done, _, err = dialog.Update(plugin.KeyEvent{Key: "up"})
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, 1, d.cursor)

	// Escape closes.
	done, _, err = dialog.Update(plugin.KeyEvent{Key: "esc"})
	require.NoError(t, err)
	require.True(t, done)
}

func TestDialogToggle(t *testing.T) {
	// Not parallel - modifies global singleton.

	cfg := Config{
		Prompts: []PromptConfig{
			{File: "a.md", Schedule: "* * * * *", Name: "A"},
		},
	}
	hook, err := NewHook(nil, cfg)
	require.NoError(t, err)

	dialog, err := NewDialog(nil)
	require.NoError(t, err)

	d := dialog.(*Dialog)

	// Initially disabled.
	require.False(t, d.allEnabled)
	require.False(t, hook.IsEnabled())

	// Toggle all (cursor at 0).
	_, _, err = dialog.Update(plugin.KeyEvent{Key: "enter"})
	require.NoError(t, err)
	require.True(t, d.allEnabled)
	require.True(t, hook.IsEnabled())

	// Toggle again.
	_, _, err = dialog.Update(plugin.KeyEvent{Key: "space"})
	require.NoError(t, err)
	require.False(t, d.allEnabled)
	require.False(t, hook.IsEnabled())
}
