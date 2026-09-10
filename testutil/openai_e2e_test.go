//go:build e2e

package testutil_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aleksclark/crush-modules/testutil"
	"github.com/stretchr/testify/require"
)

// TestGPT6AstraDistro exercises configuration, provider selection, request
// serialization, streaming, and the CLI using the actual distribution binary.
func TestGPT6AstraDistro(t *testing.T) {
	testutil.SkipIfE2EDisabled(t)
	t.Parallel()
	for _, providerType := range []string{"openai", "openai-compat"} {
		t.Run(providerType, func(t *testing.T) {
			t.Parallel()
			url, requests := newOpenAIServer(t)
			dir := t.TempDir()
			selected := map[string]any{
				"provider": "astra-test", "model": "gpt-6-astra", "max_tokens": 4096, "reasoning_effort": "xhigh",
			}
			cfg := map[string]any{
				"providers": map[string]any{"astra-test": map[string]any{
					"type": providerType, "base_url": url, "api_key": "test-key",
					"models": []any{map[string]any{
						"id": "gpt-6-astra", "name": "GPT-6 Astra", "context_window": 1050000,
						"default_max_tokens": 4096, "can_reason": true,
						"reasoning_levels":         []string{"low", "medium", "high", "xhigh", "max"},
						"default_reasoning_effort": "high",
					}},
				}},
				"models": map[string]any{"large": selected, "small": selected},
				"options": map[string]any{
					"disable_default_providers": true, "disable_provider_auto_update": true,
					"disable_metrics": true, "auto_lsp": false,
					"disabled_plugins": []string{"otlp", "agent-status", "periodic-prompts", "subagent", "tempotown", "acp-server", "a2a-server", "tavily", "kuri_fetch", "agentic_browser"},
				},
			}
			data, err := json.Marshal(cfg)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "crush.json"), data, 0o600))
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, testutil.CrushBinary(), "run", "--quiet", "test")
			cmd.Dir = dir
			// Do not inherit API keys, MCP servers, skills, or user config.
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TERM=dumb",
				"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
				"XDG_DATA_HOME=" + filepath.Join(dir, "data"),
				"XDG_CACHE_HOME=" + filepath.Join(dir, "cache"),
				"CRUSH_GLOBAL_CONFIG=" + filepath.Join(dir, "config", "crush"),
				"CRUSH_GLOBAL_DATA=" + filepath.Join(dir, "data", "crush"),
			}
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			require.Contains(t, string(output), astraReply)
			got := requests()
			require.NotEmpty(t, got)
			var streamed bool
			for _, req := range got {
				require.Equal(t, "gpt-6-astra", req.body["model"])
				require.NotContains(t, req.body, "max_tokens")
				// Title generation can also stream, but has no coding tools.
				if req.body["stream"] != true || req.body["tools"] == nil {
					continue
				}
				streamed = true
				require.NotEmpty(t, req.body["tools"], "the coding agent must still advertise tools")
				if providerType == "openai" {
					require.Equal(t, "/v1/responses", req.path)
					require.Equal(t, float64(4096), req.body["max_output_tokens"])
					require.Equal(t, map[string]any{"effort": "xhigh", "summary": "auto"}, req.body["reasoning"])
				} else {
					require.Equal(t, "/v1/chat/completions", req.path)
					require.Equal(t, float64(4096), req.body["max_completion_tokens"])
					require.Equal(t, "xhigh", req.body["reasoning_effort"])
				}
			}
			require.True(t, streamed, "the coding agent must advertise tools and stream a response")
		})
	}
}
