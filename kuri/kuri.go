// Package kuri provides Crush plugins powered by the Kuri browser toolkit.
//
// Two tools are registered:
//   - kuri_fetch: fast HTTP fetcher using kuri-fetch CLI (replaces built-in fetch)
//   - agentic_browser: autonomous browser agent using kuri-agent CLI
package kuri

import (
	"context"

	"github.com/charmbracelet/crush/plugin"
)

const (
	FetchToolName   = "kuri_fetch"
	BrowserToolName = "agentic_browser"
)

// Config defines configuration options for the kuri plugin.
type Config struct {
	KuriFetchPath string `json:"kuri_fetch_path,omitempty"`
	KuriAgentPath string `json:"kuri_agent_path,omitempty"`
	ChromePort    int    `json:"chrome_port,omitempty"`
}

func init() {
	plugin.RegisterToolWithConfig(FetchToolName, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
		var cfg Config
		if err := app.LoadConfig("kuri", &cfg); err != nil {
			return nil, err
		}
		return NewFetchTool(cfg), nil
	}, &Config{})

	plugin.RegisterToolWithConfig(BrowserToolName, func(ctx context.Context, app *plugin.App) (plugin.Tool, error) {
		var cfg Config
		if err := app.LoadConfig("kuri", &cfg); err != nil {
			return nil, err
		}
		return NewBrowserTool(app, cfg), nil
	}, &Config{})
}
