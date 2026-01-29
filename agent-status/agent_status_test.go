package agentstatus

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/crush/plugin"
	"github.com/stretchr/testify/require"
)

func TestNewAgentStatusHook(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp(
		plugin.WithWorkingDir("/tmp/test"),
	)

	hook, err := NewAgentStatusHook(app, Config{})
	require.NoError(t, err)
	require.Equal(t, HookName, hook.Name())
	require.Equal(t, StatusIdle, hook.currentStatus)
	require.NotEmpty(t, hook.instanceID)
	require.NotEmpty(t, hook.statusFilePath)
}

func TestConfigDefaults(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp()

	// Test with zero config.
	hook, err := NewAgentStatusHook(app, Config{})
	require.NoError(t, err)
	require.Equal(t, 10, hook.cfg.UpdateIntervalSeconds)

	// Test with custom interval.
	hook2, err := NewAgentStatusHook(app, Config{UpdateIntervalSeconds: 5})
	require.NoError(t, err)
	require.Equal(t, 5, hook2.cfg.UpdateIntervalSeconds)
}

func TestBuildStatusFile(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp(
		plugin.WithWorkingDir("/test/project"),
	)

	hook, err := NewAgentStatusHook(app, Config{})
	require.NoError(t, err)

	hook.currentStatus = StatusWorking
	hook.currentTask = "implementing feature"
	hook.activeTool = "edit"
	hook.recentTools = []string{"view", "grep", "edit"}
	hook.toolCounts = map[string]int{"view": 5, "grep": 2, "edit": 1}

	sf := hook.buildStatusFile()

	require.Equal(t, SchemaVersion, sf.Version)
	require.Equal(t, DefaultAgentType, sf.Agent)
	require.Equal(t, hook.instanceID, sf.Instance)
	require.Equal(t, StatusWorking, sf.Status)
	require.Equal(t, "implementing feature", sf.Task)
	require.Equal(t, "/test/project", sf.CWD)
	require.NotZero(t, sf.PID)
	require.NotZero(t, sf.Updated)
	require.NotZero(t, sf.Started)

	require.NotNil(t, sf.Tools)
	require.Equal(t, "edit", sf.Tools.Active)
	require.Equal(t, []string{"view", "grep", "edit"}, sf.Tools.Recent)
	require.Equal(t, 5, sf.Tools.Counts["view"])
}

func TestWriteStatusFile(t *testing.T) {
	// Use a temp directory for the status file.
	tmpDir := t.TempDir()
	t.Setenv("AGENT_STATUS_DIR", tmpDir)

	app := plugin.NewApp(
		plugin.WithWorkingDir("/test/project"),
	)

	hook, err := NewAgentStatusHook(app, Config{})
	require.NoError(t, err)
	// Update the path since env var was set after hook creation.
	hook.statusFilePath = filepath.Join(tmpDir, "crush-"+hook.instanceID+".json")

	hook.currentStatus = StatusThinking
	hook.currentTask = "analyzing code"

	err = hook.writeStatusFile()
	require.NoError(t, err)

	// Verify the file exists and is valid JSON.
	data, err := os.ReadFile(hook.statusFilePath)
	require.NoError(t, err)

	var sf StatusFile
	err = json.Unmarshal(data, &sf)
	require.NoError(t, err)

	require.Equal(t, SchemaVersion, sf.Version)
	require.Equal(t, DefaultAgentType, sf.Agent)
	require.Equal(t, StatusThinking, sf.Status)
	require.Equal(t, "analyzing code", sf.Task)

	// Verify file permissions.
	info, err := os.Stat(hook.statusFilePath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestRemoveStatusFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENT_STATUS_DIR", tmpDir)

	app := plugin.NewApp()

	hook, err := NewAgentStatusHook(app, Config{})
	require.NoError(t, err)
	hook.statusFilePath = filepath.Join(tmpDir, "crush-"+hook.instanceID+".json")

	// Write a file first.
	err = hook.writeStatusFile()
	require.NoError(t, err)
	require.FileExists(t, hook.statusFilePath)

	// Remove it.
	err = hook.removeStatusFile()
	require.NoError(t, err)
	require.NoFileExists(t, hook.statusFilePath)

	// Removing again should not error.
	err = hook.removeStatusFile()
	require.NoError(t, err)
}

func TestHandleMessageCreated(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp()
	hook, err := NewAgentStatusHook(app, Config{})
	require.NoError(t, err)

	// User message should set status to thinking.
	hook.handleMessageCreated(plugin.Message{
		Role:    plugin.MessageRoleUser,
		Content: "please implement this feature",
	})
	require.Equal(t, StatusThinking, hook.currentStatus)
	require.Equal(t, "please implement this feature", hook.currentTask)

	// Assistant message without tools should set status to idle.
	hook.handleMessageCreated(plugin.Message{
		Role:    plugin.MessageRoleAssistant,
		Content: "I've completed the task.",
	})
	require.Equal(t, StatusIdle, hook.currentStatus)

	// Assistant message with tools should set status to working.
	hook.currentStatus = StatusThinking
	hook.handleMessageCreated(plugin.Message{
		Role: plugin.MessageRoleAssistant,
		ToolCalls: []plugin.ToolCallInfo{
			{ID: "tc1", Name: "edit", Finished: false},
		},
	})
	require.Equal(t, StatusWorking, hook.currentStatus)
}

func TestHandleMessageUpdated(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp()
	hook, err := NewAgentStatusHook(app, Config{})
	require.NoError(t, err)

	// Update with active tool call.
	hook.handleMessageUpdated(plugin.Message{
		Role: plugin.MessageRoleAssistant,
		ToolCalls: []plugin.ToolCallInfo{
			{ID: "tc1", Name: "view", Finished: false},
		},
	})
	require.Equal(t, StatusWorking, hook.currentStatus)
	require.Equal(t, "view", hook.activeTool)
	require.Contains(t, hook.recentTools, "view")
	require.Equal(t, 1, hook.toolCounts["view"])

	// Update when tool finishes.
	hook.handleMessageUpdated(plugin.Message{
		Role: plugin.MessageRoleAssistant,
		ToolCalls: []plugin.ToolCallInfo{
			{ID: "tc1", Name: "view", Finished: true},
		},
	})
	require.Equal(t, StatusThinking, hook.currentStatus)
	require.Equal(t, "", hook.activeTool)
}

func TestAddRecentTool(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp()
	hook, err := NewAgentStatusHook(app, Config{})
	require.NoError(t, err)

	// Add tools.
	for i := 0; i < 15; i++ {
		hook.addRecentTool("tool" + string(rune('a'+i)))
	}

	// Should only keep last 10.
	require.Len(t, hook.recentTools, 10)
	require.Equal(t, "toolg", hook.recentTools[1]) // 'g' is the second in last 10

	// Adding same tool twice in a row should not duplicate.
	hook.recentTools = []string{"view"}
	hook.addRecentTool("view")
	require.Len(t, hook.recentTools, 1)
}

func TestTruncateString(t *testing.T) {
	t.Parallel()

	require.Equal(t, "short", truncateString("short", 100))
	require.Equal(t, "this is a lo...", truncateString("this is a long string", 15))
	require.Equal(t, "ab", truncateString("abcdef", 2))
}

func TestGetStatusDir(t *testing.T) {
	// With env var set.
	t.Setenv("AGENT_STATUS_DIR", "/custom/path")
	require.Equal(t, "/custom/path", getStatusDir(""))

	// Config takes precedence over env var.
	require.Equal(t, "/from/config", getStatusDir("/from/config"))
}

func TestExpandPath(t *testing.T) {
	t.Parallel()

	home, _ := os.UserHomeDir()

	// Test tilde expansion.
	require.Equal(t, filepath.Join(home, ".agent-status"), expandPath("~/.agent-status"))
	require.Equal(t, filepath.Join(home, "foo/bar"), expandPath("~/foo/bar"))

	// Test no expansion needed.
	require.Equal(t, "/absolute/path", expandPath("/absolute/path"))
	require.Equal(t, "relative/path", expandPath("relative/path"))

	// Test empty string.
	require.Equal(t, "", expandPath(""))
}

func TestGenerateInstanceID(t *testing.T) {
	t.Parallel()

	id1 := generateInstanceID()
	id2 := generateInstanceID()

	require.NotEmpty(t, id1)
	require.NotEmpty(t, id2)
	require.NotEqual(t, id1, id2)
	require.Len(t, id1, 6) // 3 bytes = 6 hex chars
}

func TestStatusFileJSONFormat(t *testing.T) {
	t.Parallel()

	sf := StatusFile{
		Version:  1,
		Agent:    "crush",
		Instance: "abc123",
		Status:   "working",
		Updated:  1737276300,
		PID:      12345,
		CWD:      "/home/user/project",
		Task:     "implementing feature",
		Started:  1737276000,
		Tools: &ToolsInfo{
			Active: "edit",
			Recent: []string{"view", "grep", "edit"},
			Counts: map[string]int{"edit": 5, "view": 12, "grep": 3},
		},
	}

	data, err := json.Marshal(sf)
	require.NoError(t, err)

	// Verify all required fields are present.
	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	require.Equal(t, float64(1), parsed["v"])
	require.Equal(t, "crush", parsed["agent"])
	require.Equal(t, "abc123", parsed["instance"])
	require.Equal(t, "working", parsed["status"])
	require.Equal(t, float64(1737276300), parsed["updated"])

	// Verify tools structure.
	tools := parsed["tools"].(map[string]any)
	require.Equal(t, "edit", tools["active"])
}

// TestHookStartAndStop tests the hook lifecycle.
func TestHookStartAndStop(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENT_STATUS_DIR", tmpDir)

	app := plugin.NewApp(
		plugin.WithWorkingDir("/test"),
	)

	hook, err := NewAgentStatusHook(app, Config{UpdateIntervalSeconds: 1})
	require.NoError(t, err)
	hook.statusFilePath = filepath.Join(tmpDir, "crush-"+hook.instanceID+".json")

	ctx, cancel := context.WithCancel(context.Background())

	// Start in background.
	done := make(chan error, 1)
	go func() {
		done <- hook.Start(ctx)
	}()

	// Wait for the file to be created.
	require.Eventually(t, func() bool {
		_, err := os.Stat(hook.statusFilePath)
		return err == nil
	}, 2*time.Second, 100*time.Millisecond)

	// Verify it's valid JSON.
	data, err := os.ReadFile(hook.statusFilePath)
	require.NoError(t, err)

	var sf StatusFile
	require.NoError(t, json.Unmarshal(data, &sf))
	require.Equal(t, StatusIdle, sf.Status)

	// Stop the hook.
	cancel()

	// Wait for it to finish.
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("hook did not stop in time")
	}

	// File should be removed.
	require.NoFileExists(t, hook.statusFilePath)
}
