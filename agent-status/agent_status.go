// Package agentstatus provides an agent status reporting plugin for Crush.
//
// This plugin implements the agent status reporting protocol defined at:
// https://github.com/aleksclark/go-turing-smart-screen/blob/master/AGENT_STATUS_REPORTING.md
//
// It writes a JSON status file to ~/.agent-status/ (or $AGENT_STATUS_DIR) that can be
// read by external tools to monitor the agent's current state. The file is updated
// at least every 10 seconds (configurable) and on status changes.
//
// Configuration in crush.json:
//
//	{
//	  "options": {
//	    "plugins": {
//	      "agent-status": {
//	        "status_dir": "~/.agent-status",
//	        "update_interval_seconds": 10
//	      }
//	    }
//	  }
//	}
//
// The status_dir supports ~ for home directory expansion.
package agentstatus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/charmbracelet/crush/plugin"
)

const (
	// HookName is the name of the agent-status hook.
	HookName = "agent-status"

	// DefaultUpdateInterval is the default interval for status file updates.
	DefaultUpdateInterval = 10 * time.Second

	// DefaultAgentType is the agent type identifier.
	DefaultAgentType = "crush"

	// SchemaVersion is the current schema version.
	SchemaVersion = 1
)

// Status values as defined by the protocol.
const (
	StatusIdle     = "idle"
	StatusThinking = "thinking"
	StatusWorking  = "working"
	StatusWaiting  = "waiting"
	StatusError    = "error"
	StatusDone     = "done"
	StatusPaused   = "paused"
)

// Config defines the configuration options for the agent-status plugin.
type Config struct {
	// UpdateIntervalSeconds is how often to update the status file.
	// Default is 10 seconds.
	UpdateIntervalSeconds int `json:"update_interval_seconds,omitempty"`

	// StatusDir is the directory where status files are written.
	// Supports ~ for home directory expansion.
	// Defaults to ~/.agent-status or $AGENT_STATUS_DIR.
	StatusDir string `json:"status_dir,omitempty"`
}

// StatusFile represents the JSON structure written to the status file.
type StatusFile struct {
	// Required fields.
	Version  int    `json:"v"`
	Agent    string `json:"agent"`
	Instance string `json:"instance"`
	Status   string `json:"status"`
	Updated  int64  `json:"updated"`

	// Optional fields.
	PID     int    `json:"pid,omitempty"`
	Project string `json:"project,omitempty"`
	CWD     string `json:"cwd,omitempty"`
	Task    string `json:"task,omitempty"`
	Model   string `json:"model,omitempty"`
	Started int64  `json:"started,omitempty"`
	Error   string `json:"error,omitempty"`

	// Tool tracking.
	Tools *ToolsInfo `json:"tools,omitempty"`
}

// ToolsInfo contains tool usage information.
type ToolsInfo struct {
	Active string         `json:"active,omitempty"`
	Recent []string       `json:"recent,omitempty"`
	Counts map[string]int `json:"counts,omitempty"`
}

func init() {
	plugin.RegisterHookWithConfig(HookName, func(ctx context.Context, app *plugin.App) (plugin.Hook, error) {
		var cfg Config
		if err := app.LoadConfig(HookName, &cfg); err != nil {
			return nil, err
		}
		return NewAgentStatusHook(app, cfg)
	}, &Config{})
}

// AgentStatusHook implements the plugin.Hook interface for agent status reporting.
type AgentStatusHook struct {
	app            *plugin.App
	cfg            Config
	logger         *slog.Logger
	instanceID     string
	statusFilePath string
	startedAt      int64

	mu            sync.RWMutex
	currentStatus string
	currentTask   string
	activeTool    string
	recentTools   []string
	toolCounts    map[string]int
	lastError     string
}

// NewAgentStatusHook creates a new agent status reporting hook.
func NewAgentStatusHook(app *plugin.App, cfg Config) (*AgentStatusHook, error) {
	if cfg.UpdateIntervalSeconds <= 0 {
		cfg.UpdateIntervalSeconds = int(DefaultUpdateInterval.Seconds())
	}

	instanceID := generateInstanceID()
	statusDir := getStatusDir(cfg.StatusDir)
	statusFilePath := filepath.Join(statusDir, fmt.Sprintf("%s-%s.json", DefaultAgentType, instanceID))

	hook := &AgentStatusHook{
		app:            app,
		cfg:            cfg,
		logger:         app.Logger().With("hook", HookName),
		instanceID:     instanceID,
		statusFilePath: statusFilePath,
		startedAt:      time.Now().Unix(),
		currentStatus:  StatusIdle,
		recentTools:    make([]string, 0, 10),
		toolCounts:     make(map[string]int),
	}

	return hook, nil
}

// Name returns the hook identifier.
func (h *AgentStatusHook) Name() string {
	return HookName
}

// Start begins the status reporting loop.
func (h *AgentStatusHook) Start(ctx context.Context) error {
	// Ensure status directory exists.
	statusDir := filepath.Dir(h.statusFilePath)
	if err := os.MkdirAll(statusDir, 0o700); err != nil {
		return fmt.Errorf("failed to create status directory: %w", err)
	}

	// Write initial status.
	if err := h.writeStatusFile(); err != nil {
		h.logger.Error("failed to write initial status file", "error", err)
	}

	// Register cleanup to remove status file on shutdown.
	h.app.RegisterCleanup(func() error {
		return h.removeStatusFile()
	})

	// Subscribe to message events.
	messages := h.app.Messages()
	var events <-chan plugin.MessageEvent
	if messages != nil {
		events = messages.SubscribeMessages(ctx)
	}

	// Create ticker for periodic updates.
	ticker := time.NewTicker(time.Duration(h.cfg.UpdateIntervalSeconds) * time.Second)
	defer ticker.Stop()

	h.logger.Info("agent status reporting started",
		"status_file", h.statusFilePath,
		"update_interval", h.cfg.UpdateIntervalSeconds,
	)

	for {
		select {
		case <-ctx.Done():
			return h.Stop()
		case <-ticker.C:
			if err := h.writeStatusFile(); err != nil {
				h.logger.Error("failed to write status file", "error", err)
			}
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			h.handleEvent(event)
			// Write status immediately after state changes.
			if err := h.writeStatusFile(); err != nil {
				h.logger.Error("failed to write status file", "error", err)
			}
		}
	}
}

// Stop gracefully shuts down the hook.
func (h *AgentStatusHook) Stop() error {
	h.logger.Info("agent status reporting stopped")
	return h.removeStatusFile()
}

func (h *AgentStatusHook) handleEvent(event plugin.MessageEvent) {
	msg := event.Message

	h.mu.Lock()
	defer h.mu.Unlock()

	switch event.Type {
	case plugin.MessageCreated:
		h.handleMessageCreated(msg)
	case plugin.MessageUpdated:
		h.handleMessageUpdated(msg)
	}
}

func (h *AgentStatusHook) handleMessageCreated(msg plugin.Message) {
	switch msg.Role {
	case plugin.MessageRoleUser:
		// User sent a message, agent is now thinking.
		h.currentStatus = StatusThinking
		h.currentTask = truncateString(msg.Content, 100)
		h.activeTool = ""
		h.lastError = ""
	case plugin.MessageRoleAssistant:
		// Assistant responded, check if there are tool calls.
		if len(msg.ToolCalls) > 0 {
			h.currentStatus = StatusWorking
		} else {
			// No tool calls, response complete, back to idle.
			h.currentStatus = StatusIdle
		}
	case plugin.MessageRoleTool:
		// Tool results came back.
		for _, tr := range msg.ToolResults {
			if tr.IsError {
				h.lastError = truncateString(tr.Content, 200)
			}
		}
		// After tool results, we're thinking about the next step.
		h.currentStatus = StatusThinking
		h.activeTool = ""
	}
}

func (h *AgentStatusHook) handleMessageUpdated(msg plugin.Message) {
	if msg.Role != plugin.MessageRoleAssistant {
		return
	}

	// Track tool calls.
	for _, tc := range msg.ToolCalls {
		if !tc.Finished {
			h.currentStatus = StatusWorking
			h.activeTool = tc.Name
			h.addRecentTool(tc.Name)
			h.toolCounts[tc.Name]++
		} else {
			// Tool finished, might have more or be done.
			if h.activeTool == tc.Name {
				h.activeTool = ""
			}
		}
	}

	// Check if all tool calls are finished.
	allFinished := true
	for _, tc := range msg.ToolCalls {
		if !tc.Finished {
			allFinished = false
			break
		}
	}
	if allFinished && len(msg.ToolCalls) > 0 {
		h.currentStatus = StatusThinking
	}
}

func (h *AgentStatusHook) addRecentTool(name string) {
	// Keep only the last 10 tools.
	if len(h.recentTools) >= 10 {
		h.recentTools = h.recentTools[1:]
	}
	// Avoid duplicates in a row.
	if len(h.recentTools) > 0 && h.recentTools[len(h.recentTools)-1] == name {
		return
	}
	h.recentTools = append(h.recentTools, name)
}

func (h *AgentStatusHook) writeStatusFile() error {
	h.mu.RLock()
	status := h.buildStatusFile()
	h.mu.RUnlock()

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal status: %w", err)
	}

	// Write atomically by writing to temp file and renaming.
	tmpFile := h.statusFilePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write temp status file: %w", err)
	}

	if err := os.Rename(tmpFile, h.statusFilePath); err != nil {
		os.Remove(tmpFile) // Clean up on failure.
		return fmt.Errorf("failed to rename status file: %w", err)
	}

	return nil
}

func (h *AgentStatusHook) buildStatusFile() StatusFile {
	sf := StatusFile{
		Version:  SchemaVersion,
		Agent:    DefaultAgentType,
		Instance: h.instanceID,
		Status:   h.currentStatus,
		Updated:  time.Now().Unix(),
		PID:      os.Getpid(),
		CWD:      h.app.WorkingDir(),
		Started:  h.startedAt,
	}

	if h.currentTask != "" {
		sf.Task = h.currentTask
	}

	if h.lastError != "" && h.currentStatus == StatusError {
		sf.Error = h.lastError
	}

	// Include tool info if we have any.
	if h.activeTool != "" || len(h.recentTools) > 0 || len(h.toolCounts) > 0 {
		sf.Tools = &ToolsInfo{
			Active: h.activeTool,
			Recent: h.recentTools,
			Counts: h.toolCounts,
		}
	}

	return sf
}

func (h *AgentStatusHook) removeStatusFile() error {
	if err := os.Remove(h.statusFilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove status file: %w", err)
	}
	h.logger.Debug("removed status file", "path", h.statusFilePath)
	return nil
}

// getStatusDir returns the directory for status files.
// The configDir parameter allows overriding via configuration.
func getStatusDir(configDir string) string {
	// Config takes precedence.
	if configDir != "" {
		return expandPath(configDir)
	}
	// Then environment variable.
	if dir := os.Getenv("AGENT_STATUS_DIR"); dir != "" {
		return expandPath(dir)
	}
	// Default to ~/.agent-status.
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/.agent-status"
	}
	return filepath.Join(home, ".agent-status")
}

// expandPath expands ~ to the user's home directory.
func expandPath(path string) string {
	if len(path) == 0 {
		return path
	}
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}

// generateInstanceID generates a short unique instance identifier.
func generateInstanceID() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		// Fallback to PID if random fails.
		return fmt.Sprintf("p%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}

// truncateString truncates a string to maxLen characters, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
