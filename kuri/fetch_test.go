package kuri

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func TestFetchToolMetadata(t *testing.T) {
	t.Parallel()

	tool := NewFetchTool(Config{}, t.TempDir())
	require.Equal(t, FetchToolName, tool.Info().Name)
	require.NotEmpty(t, tool.Info().Description)
}

func TestFetchToolRequiresURL(t *testing.T) {
	t.Parallel()

	tool := NewFetchTool(Config{}, t.TempDir())
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

	dir := t.TempDir()
	tool := NewFetchTool(Config{}, dir)
	call := fantasy.ToolCall{
		ID:    "test-2",
		Name:  FetchToolName,
		Input: `{"url": "example.com", "format": "text"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError, "expected success but got: %s", resp.Content)
	require.Contains(t, resp.Content, "Successfully saved")
	require.Contains(t, resp.Content, "example.com.txt")

	data, err := os.ReadFile(filepath.Join(dir, "example.com.txt"))
	require.NoError(t, err)
	require.Contains(t, string(data), "Example Domain")
}

func TestFetchToolFormats(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("kuri-fetch"); err != nil {
		t.Skip("kuri-fetch not installed")
	}

	expectedExts := map[string]string{
		"text":     ".txt",
		"markdown": ".md",
		"html":     ".html",
		"json":     ".json",
		"links":    ".txt",
	}

	for _, format := range []string{"text", "markdown", "html", "json", "links"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tool := NewFetchTool(Config{}, dir)
			call := fantasy.ToolCall{
				ID:    "test-fmt-" + format,
				Name:  FetchToolName,
				Input: `{"url": "https://example.com", "format": "` + format + `"}`,
			}

			resp, err := tool.Run(context.Background(), call)
			require.NoError(t, err)
			require.False(t, resp.IsError, "format %s failed: %s", format, resp.Content)
			require.Contains(t, resp.Content, "Successfully saved")
			require.Contains(t, resp.Content, expectedExts[format])
		})
	}
}

func TestFetchToolCustomBinaryPath(t *testing.T) {
	t.Parallel()

	tool := NewFetchTool(Config{KuriFetchPath: "/nonexistent/kuri-fetch"}, t.TempDir())
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

	if _, err := exec.LookPath("kuri-fetch"); err != nil {
		t.Skip("kuri-fetch not installed")
	}

	tool := NewFetchTool(Config{}, t.TempDir())
	call := fantasy.ToolCall{
		ID:    "test-4",
		Name:  FetchToolName,
		Input: `{"url": "https://example.com", "timeout": 200}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Successfully saved")
}

func TestFetchToolJSFlag(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("kuri-fetch"); err != nil {
		t.Skip("kuri-fetch not installed")
	}

	dir := t.TempDir()
	tool := NewFetchTool(Config{}, dir)
	call := fantasy.ToolCall{
		ID:    "test-5",
		Name:  FetchToolName,
		Input: `{"url": "https://example.com", "js": true, "format": "text"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Successfully saved")

	data, err := os.ReadFile(filepath.Join(dir, "example.com.txt"))
	require.NoError(t, err)
	require.Contains(t, string(data), "Example Domain")
}

func TestFetchToolDefaultFormat(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("kuri-fetch"); err != nil {
		t.Skip("kuri-fetch not installed")
	}

	dir := t.TempDir()
	tool := NewFetchTool(Config{}, dir)
	call := fantasy.ToolCall{
		ID:    "test-6",
		Name:  FetchToolName,
		Input: `{"url": "https://example.com"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Successfully saved")
	// Default format is markdown -> .md extension.
	require.Contains(t, resp.Content, ".md")

	data, err := os.ReadFile(filepath.Join(dir, "example.com.md"))
	require.NoError(t, err)
	require.NotEmpty(t, data)
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

func TestFetchToolWritesToFile(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("kuri-fetch"); err != nil {
		t.Skip("kuri-fetch not installed")
	}

	dir := t.TempDir()
	tool := NewFetchTool(Config{}, dir)
	call := fantasy.ToolCall{
		ID:    "test-file-1",
		Name:  FetchToolName,
		Input: `{"url": "https://example.com", "format": "text", "file_path": "output.txt"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError, "expected success but got: %s", resp.Content)
	require.Contains(t, resp.Content, "Successfully saved")
	require.Contains(t, resp.Content, "output.txt")

	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	require.Contains(t, string(data), "Example Domain")
}

func TestFetchToolWritesToFileCreatesParentDirs(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("kuri-fetch"); err != nil {
		t.Skip("kuri-fetch not installed")
	}

	dir := t.TempDir()
	tool := NewFetchTool(Config{}, dir)
	call := fantasy.ToolCall{
		ID:    "test-file-2",
		Name:  FetchToolName,
		Input: `{"url": "https://example.com", "format": "text", "file_path": "sub/dir/output.txt"}`,
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError, "expected success but got: %s", resp.Content)

	data, err := os.ReadFile(filepath.Join(dir, "sub", "dir", "output.txt"))
	require.NoError(t, err)
	require.Contains(t, string(data), "Example Domain")
}

func TestFetchToolWritesToFileAbsolutePath(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("kuri-fetch"); err != nil {
		t.Skip("kuri-fetch not installed")
	}

	dir := t.TempDir()
	absPath := filepath.Join(dir, "abs-output.txt")
	tool := NewFetchTool(Config{}, t.TempDir())
	call := fantasy.ToolCall{
		ID:    "test-file-3",
		Name:  FetchToolName,
		Input: fmt.Sprintf(`{"url": "https://example.com", "format": "text", "file_path": %q}`, absPath),
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)
	require.False(t, resp.IsError, "expected success but got: %s", resp.Content)

	data, err := os.ReadFile(absPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "Example Domain")
}

func TestFilenameFromURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url    string
		format string
		want   string
	}{
		{"https://example.com", "text", "example.com.txt"},
		{"https://example.com", "markdown", "example.com.md"},
		{"https://example.com", "html", "example.com.html"},
		{"https://example.com", "json", "example.com.json"},
		{"https://example.com", "links", "example.com.txt"},
		{"https://example.com/foo/bar", "text", "example.com_foo_bar.txt"},
		{"https://example.com/foo/bar/", "text", "example.com_foo_bar.txt"},
		{"not-a-url", "text", "fetch-output.txt"},
		{"https://example.com:8080/api", "json", "example.com_8080_api.json"},
	}

	for _, tt := range tests {
		t.Run(tt.url+"_"+tt.format, func(t *testing.T) {
			t.Parallel()
			got := filenameFromURL(tt.url, tt.format)
			require.Equal(t, tt.want, got)
		})
	}
}
