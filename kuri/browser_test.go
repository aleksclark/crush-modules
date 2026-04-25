package kuri

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/plugin"
	"github.com/stretchr/testify/require"
)

func TestBrowserToolMetadata(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp()
	tool := NewBrowserTool(app, Config{})
	require.Equal(t, BrowserToolName, tool.Info().Name)
	require.NotEmpty(t, tool.Info().Description)
}

func TestBrowserToolRequiresPrompt(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp()
	tool := NewBrowserTool(app, Config{})
	call := fantasy.ToolCall{
		ID:    "test-1",
		Name:  BrowserToolName,
		Input: `{"url": "https://example.com"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "prompt is required")
}

func TestBrowserToolRequiresSubAgentRunner(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp()
	tool := NewBrowserTool(app, Config{})
	call := fantasy.ToolCall{
		ID:    "test-2",
		Name:  BrowserToolName,
		Input: `{"prompt": "go to example.com and get the title"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "sub-agent runner not available")
}

type mockSubAgentRunner struct {
	lastOpts plugin.SubAgentOptions
	response string
	err      error
}

func (m *mockSubAgentRunner) RunSubAgent(_ context.Context, opts plugin.SubAgentOptions) (string, error) {
	m.lastOpts = opts
	return m.response, m.err
}

func TestBrowserToolWithMockRunner(t *testing.T) {
	t.Parallel()

	runner := &mockSubAgentRunner{response: "The page title is 'Example Domain'"}
	app := plugin.NewApp(plugin.WithSubAgentRunner(runner))
	tool := NewBrowserTool(app, Config{})

	call := fantasy.ToolCall{
		ID:    "test-3",
		Name:  BrowserToolName,
		Input: `{"url": "https://example.com", "prompt": "get the page title"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Example Domain")

	require.Equal(t, "browser-agent", runner.lastOpts.Name)
	require.Contains(t, runner.lastOpts.Prompt, "Navigate to https://example.com")
	require.Contains(t, runner.lastOpts.Prompt, "get the page title")
	require.Equal(t, []string{"bash"}, runner.lastOpts.AllowedTools)
	require.Contains(t, runner.lastOpts.SystemPrompt, "kuri-agent")
}

func TestBrowserToolPromptOnlyMode(t *testing.T) {
	t.Parallel()

	runner := &mockSubAgentRunner{response: "Found the information"}
	app := plugin.NewApp(plugin.WithSubAgentRunner(runner))
	tool := NewBrowserTool(app, Config{})

	call := fantasy.ToolCall{
		ID:    "test-4",
		Name:  BrowserToolName,
		Input: `{"prompt": "search for Go programming tutorials"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError)

	require.Equal(t, "search for Go programming tutorials", runner.lastOpts.Prompt)
}

func TestBrowserToolCustomConfig(t *testing.T) {
	t.Parallel()

	runner := &mockSubAgentRunner{response: "ok"}
	app := plugin.NewApp(plugin.WithSubAgentRunner(runner))
	tool := NewBrowserTool(app, Config{
		KuriAgentPath: "/custom/kuri-agent",
		ChromePort:    9333,
	})

	call := fantasy.ToolCall{
		ID:    "test-5",
		Name:  BrowserToolName,
		Input: `{"prompt": "test"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError)

	require.Contains(t, runner.lastOpts.SystemPrompt, "/custom/kuri-agent")
	require.Contains(t, runner.lastOpts.SystemPrompt, "9333")
}

func TestBrowserToolSubAgentError(t *testing.T) {
	t.Parallel()

	runner := &mockSubAgentRunner{err: context.DeadlineExceeded}
	app := plugin.NewApp(plugin.WithSubAgentRunner(runner))
	tool := NewBrowserTool(app, Config{})

	call := fantasy.ToolCall{
		ID:    "test-6",
		Name:  BrowserToolName,
		Input: `{"prompt": "do something"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "browser agent error")
}
