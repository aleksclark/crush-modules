// Package periodicprompts provides a Crush plugin for scheduled prompt execution.
// It allows users to configure cron-style schedules for prompts that are
// automatically sent to the LLM when periodic prompting is enabled.
package periodicprompts

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/crush/plugin"
	"github.com/robfig/cron/v3"
)

const (
	// HookName is the name of this plugin.
	HookName = "periodic-prompts"

	// ToolName is the name of the toggle tool.
	ToolName = "periodic_prompts"

	// Description is shown to the LLM.
	Description = `Controls periodic prompts that run on a cron schedule.

<usage>
- Use action "status" to check if periodic prompting is enabled and see scheduled prompts
- Use action "enable" to turn on periodic prompting
- Use action "disable" to turn off periodic prompting
- Use action "list" to see all configured periodic prompts
</usage>

<examples>
periodic_prompts(action: "status") -> Shows current state
periodic_prompts(action: "enable") -> Enables periodic prompting
periodic_prompts(action: "disable") -> Disables periodic prompting
periodic_prompts(action: "list") -> Lists configured prompts and schedules
</examples>
`
)

// Config defines configuration options for this plugin.
// Users configure this in crush.json under options.plugins.periodic-prompts
type Config struct {
	// Prompts is the list of scheduled prompts.
	Prompts []PromptConfig `json:"prompts,omitempty"`
}

// PromptConfig defines a single scheduled prompt.
type PromptConfig struct {
	// File is the path to the prompt file (supports ~ expansion).
	File string `json:"file"`
	// Schedule is a crontab-style schedule (e.g., "*/30 * * * *").
	Schedule string `json:"schedule"`
	// Name is an optional friendly name for the prompt.
	Name string `json:"name,omitempty"`
}

// ToolParams defines the parameters the LLM can pass to the toggle tool.
type ToolParams struct {
	// Action is the operation to perform: "status", "enable", "disable", "list".
	Action string `json:"action" jsonschema:"description=Action to perform: status, enable, disable, or list"`
}

// Hook implements the periodic prompts hook.
type Hook struct {
	app     *plugin.App
	cfg     Config
	cron    *cron.Cron
	enabled bool
	mu      sync.RWMutex

	// promptSubmitter allows sending prompts to the agent.
	promptSubmitter plugin.PromptSubmitter
}

func init() {
	// Register the hook for background scheduling.
	plugin.RegisterHookWithConfig(HookName, func(ctx context.Context, app *plugin.App) (plugin.Hook, error) {
		var cfg Config
		if err := app.LoadConfig(HookName, &cfg); err != nil {
			return nil, err
		}
		return NewHook(app, cfg)
	}, &Config{})

	// Register the tool for enabling/disabling via chat.
	plugin.RegisterToolWithConfig(ToolName, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
		// The tool retrieves the hook instance to control it.
		// For now, return a tool that can find the hook via a global.
		return NewTool(app), nil
	}, &Config{})
}

// hookInstance holds the singleton hook instance for tool access.
var (
	hookInstance *Hook
	hookMu       sync.RWMutex
)

// NewHook creates a new periodic prompts hook.
func NewHook(app *plugin.App, cfg Config) (*Hook, error) {
	h := &Hook{
		app:     app,
		cfg:     cfg,
		enabled: false, // Disabled by default
	}

	// Store the singleton for tool access.
	hookMu.Lock()
	hookInstance = h
	hookMu.Unlock()

	return h, nil
}

// Name returns the hook name.
func (h *Hook) Name() string {
	return HookName
}

// logger returns the app logger or a default logger.
func (h *Hook) logger() *slog.Logger {
	if h.app != nil {
		return h.logger()
	}
	return slog.Default()
}

// Start begins the cron scheduler.
func (h *Hook) Start(ctx context.Context) error {
	// Get the prompt submitter from the app (if available).
	if h.app != nil {
		h.promptSubmitter = h.app.PromptSubmitter()
		if h.promptSubmitter == nil {
			h.logger().Warn("periodic-prompts: no prompt submitter available, prompts will not be sent")
		}
	}

	// Create cron scheduler with second precision.
	h.cron = cron.New(cron.WithParser(cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)))

	// Schedule all configured prompts.
	for i, p := range h.cfg.Prompts {
		prompt := p // Capture for closure.
		idx := i

		_, err := h.cron.AddFunc(prompt.Schedule, func() {
			h.mu.RLock()
			enabled := h.enabled
			h.mu.RUnlock()

			if !enabled {
				return
			}

			h.executePrompt(idx, prompt)
		})
		if err != nil {
			h.logger().Error("periodic-prompts: invalid schedule",
				"file", prompt.File,
				"schedule", prompt.Schedule,
				"error", err,
			)
			continue
		}

		h.logger().Info("periodic-prompts: scheduled prompt",
			"file", prompt.File,
			"schedule", prompt.Schedule,
		)
	}

	h.cron.Start()

	// Wait for context cancellation.
	<-ctx.Done()
	return h.Stop()
}

// Stop halts the cron scheduler.
func (h *Hook) Stop() error {
	if h.cron != nil {
		h.cron.Stop()
	}
	return nil
}

// executePrompt reads and submits a prompt file.
func (h *Hook) executePrompt(idx int, p PromptConfig) {
	if h.promptSubmitter == nil {
		h.logger().Warn("periodic-prompts: cannot send prompt, no submitter available",
			"file", p.File,
		)
		return
	}

	content, err := h.readPromptFile(p.File)
	if err != nil {
		h.logger().Error("periodic-prompts: failed to read prompt file",
			"file", p.File,
			"error", err,
		)
		return
	}

	name := p.Name
	if name == "" {
		name = filepath.Base(p.File)
	}

	h.logger().Info("periodic-prompts: executing scheduled prompt",
		"name", name,
		"file", p.File,
	)

	// Submit the prompt (will be queued if agent is busy).
	if err := h.promptSubmitter.SubmitPrompt(context.Background(), content); err != nil {
		h.logger().Error("periodic-prompts: failed to submit prompt",
			"file", p.File,
			"error", err,
		)
	}
}

// readPromptFile reads and returns the content of a prompt file.
func (h *Hook) readPromptFile(path string) (string, error) {
	// Expand ~ to home directory.
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot expand ~: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(content)), nil
}

// SetEnabled enables or disables periodic prompting.
func (h *Hook) SetEnabled(enabled bool) {
	h.mu.Lock()
	h.enabled = enabled
	h.mu.Unlock()

	status := "disabled"
	if enabled {
		status = "enabled"
	}
	h.logger().Info("periodic-prompts: " + status)
}

// IsEnabled returns whether periodic prompting is enabled.
func (h *Hook) IsEnabled() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.enabled
}

// GetPrompts returns the configured prompts.
func (h *Hook) GetPrompts() []PromptConfig {
	return h.cfg.Prompts
}

// getHook returns the singleton hook instance.
func getHook() *Hook {
	hookMu.RLock()
	defer hookMu.RUnlock()
	return hookInstance
}
