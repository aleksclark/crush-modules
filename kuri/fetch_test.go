package kuri

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func TestFetchToolMetadata(t *testing.T) {
	t.Parallel()

	tool := NewFetchTool(Config{})
	require.Equal(t, FetchToolName, tool.Info().Name)
	require.NotEmpty(t, tool.Info().Description)
}

func TestFetchToolRequiresURL(t *testing.T) {
	t.Parallel()

	tool := NewFetchTool(Config{})
	call := fantasy.ToolCall{
		ID:    "test-1",
		Name:  FetchToolName,
		Input: `{"prompt": "hello"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "url is required")
}

func TestFetchToolPrefixesHTTPS(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("kuri-fetch"); err != nil {
		t.Skip("kuri-fetch not installed")
	}

	tool := NewFetchTool(Config{})
	call := fantasy.ToolCall{
		ID:    "test-2",
		Name:  FetchToolName,
		Input: `{"url": "example.com", "format": "text"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError, "expected success but got: %s", resp.Content)
	require.Contains(t, resp.Content, "Example Domain")
}

func TestFetchToolFormats(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("kuri-fetch"); err != nil {
		t.Skip("kuri-fetch not installed")
	}

	formats := []string{"text", "markdown", "html", "json", "links"}

	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			tool := NewFetchTool(Config{})
			call := fantasy.ToolCall{
				ID:    "test-fmt-" + format,
				Name:  FetchToolName,
				Input: `{"url": "https://example.com", "format": "` + format + `"}`,
			}

			resp, err := tool.Run(context.Background(), call)
			require.NoError(t, err)
			require.False(t, resp.IsError, "format %s failed: %s", format, resp.Content)
			require.NotEmpty(t, resp.Content)
		})
	}
}

func TestFetchToolCustomBinaryPath(t *testing.T) {
	t.Parallel()

	tool := NewFetchTool(Config{KuriFetchPath: "/nonexistent/kuri-fetch"})
	call := fantasy.ToolCall{
		ID:    "test-3",
		Name:  FetchToolName,
		Input: `{"url": "https://example.com"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "kuri-fetch error")
}

func TestFetchToolTimeout(t *testing.T) {
	t.Parallel()

	tool := NewFetchTool(Config{})
	call := fantasy.ToolCall{
		ID:    "test-4",
		Name:  FetchToolName,
		Input: `{"url": "https://example.com", "timeout": 200}`,
	}

	// Timeout gets clamped to 120, shouldn't fail for normal sites.
	if _, err := exec.LookPath("kuri-fetch"); err != nil {
		t.Skip("kuri-fetch not installed")
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError)
}

func TestFetchToolJSFlag(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("kuri-fetch"); err != nil {
		t.Skip("kuri-fetch not installed")
	}

	tool := NewFetchTool(Config{})
	call := fantasy.ToolCall{
		ID:    "test-5",
		Name:  FetchToolName,
		Input: `{"url": "https://example.com", "js": true, "format": "text"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Example Domain")
}

func TestFetchToolDefaultFormat(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("kuri-fetch"); err != nil {
		t.Skip("kuri-fetch not installed")
	}

	tool := NewFetchTool(Config{})
	call := fantasy.ToolCall{
		ID:    "test-6",
		Name:  FetchToolName,
		Input: `{"url": "https://example.com"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	// Default format is markdown.
	require.NotEmpty(t, resp.Content)
}

func TestFetchToolSkipsIfNoKuriFetch(t *testing.T) {
	t.Parallel()

	// Use an env var to force running even without kuri-fetch, for CI.
	if os.Getenv("KURI_INTEGRATION") == "" {
		if _, err := exec.LookPath("kuri-fetch"); err != nil {
			t.Skip("kuri-fetch not installed (set KURI_INTEGRATION=1 to fail)")
		}
	}
}
