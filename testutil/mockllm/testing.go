package mockllm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestConfig creates config JSON that points to the mock server.
func TestConfig(serverURL string) string {
	return `{
  "providers": {
    "mock": {
      "type": "openai-compat",
      "base_url": "` + serverURL + `",
      "api_key": "mock-key",
      "models": [
        {
          "id": "mock-model",
          "name": "Mock Model",
          "context_window": 128000,
          "default_max_tokens": 4096,
          "can_reason": false,
          "supports_attachments": false
        }
      ]
    }
  },
  "models": {
    "large": { "provider": "mock", "model": "mock-model" },
    "small": { "provider": "mock", "model": "mock-model" }
  }
}`
}

// SetupTestEnv creates an isolated test environment with the mock LLM server.
// Returns the tmpDir for use with NewIsolatedTerminalWithConfigAndEnv.
func SetupTestEnv(t *testing.T, serverURL string) string {
	t.Helper()

	tmpDir := t.TempDir()

	// Create config directory and write config file.
	configPath := filepath.Join(tmpDir, "config", "crush")
	if err := os.MkdirAll(configPath, 0o755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	configFile := filepath.Join(configPath, "crush.json")
	if err := os.WriteFile(configFile, []byte(TestConfig(serverURL)), 0o644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Create data directory and write data config file.
	// This is required to skip the onboarding flow.
	dataPath := filepath.Join(tmpDir, "data", "crush")
	if err := os.MkdirAll(dataPath, 0o755); err != nil {
		t.Fatalf("Failed to create data dir: %v", err)
	}
	dataFile := filepath.Join(dataPath, "crush.json")
	if err := os.WriteFile(dataFile, []byte(TestConfig(serverURL)), 0o644); err != nil {
		t.Fatalf("Failed to write data config: %v", err)
	}

	return tmpDir
}

// SetupTestEnvWithConfig creates an isolated test environment with custom config.
// Merges the provided config with mock LLM settings.
func SetupTestEnvWithConfig(t *testing.T, serverURL string, additionalConfig map[string]any) string {
	t.Helper()

	tmpDir := t.TempDir()

	// Build the config.
	config := map[string]any{
		"providers": map[string]any{
			"mock": map[string]any{
				"type":     "openai-compat",
				"base_url": serverURL,
				"api_key":  "mock-key",
				"models": []map[string]any{
					{
						"id":                 "mock-model",
						"name":               "Mock Model",
						"context_window":     128000,
						"default_max_tokens": 4096,
						"can_reason":         false,
						"supports_attachments": false,
					},
				},
			},
		},
		"models": map[string]any{
			"large": map[string]any{"provider": "mock", "model": "mock-model"},
			"small": map[string]any{"provider": "mock", "model": "mock-model"},
		},
	}

	// Merge additional config.
	for k, v := range additionalConfig {
		config[k] = v
	}

	configJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	// Create config directory and write config file.
	configPath := filepath.Join(tmpDir, "config", "crush")
	if err := os.MkdirAll(configPath, 0o755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	configFile := filepath.Join(configPath, "crush.json")
	if err := os.WriteFile(configFile, configJSON, 0o644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Create data directory and write data config file.
	dataPath := filepath.Join(tmpDir, "data", "crush")
	if err := os.MkdirAll(dataPath, 0o755); err != nil {
		t.Fatalf("Failed to create data dir: %v", err)
	}
	dataFile := filepath.Join(dataPath, "crush.json")
	if err := os.WriteFile(dataFile, configJSON, 0o644); err != nil {
		t.Fatalf("Failed to write data config: %v", err)
	}

	return tmpDir
}

// Conversation is a helper for building multi-turn conversations in tests.
type Conversation struct {
	server    *Server
	responses []ResponseFunc
}

// NewConversation creates a new conversation builder.
func NewConversation(server *Server) *Conversation {
	return &Conversation{server: server}
}

// Then adds the next response in the conversation.
func (c *Conversation) Then(respond ResponseFunc) *Conversation {
	c.responses = append(c.responses, respond)
	return c
}

// ThenText adds a text response.
func (c *Conversation) ThenText(content string) *Conversation {
	return c.Then(TextResponse(content))
}

// ThenTool adds a tool call response.
func (c *Conversation) ThenTool(name string, args any) *Conversation {
	return c.Then(ToolCallResponse(name, args))
}

// ThenError adds an error response.
func (c *Conversation) ThenError(message string) *Conversation {
	return c.Then(ErrorResponse(message))
}

// Apply sets up the conversation on the server.
func (c *Conversation) Apply() {
	c.server.Sequence(c.responses...)
}

// AssertRequestCount checks the number of requests made to the server.
func AssertRequestCount(t *testing.T, server *Server, expected int) {
	t.Helper()
	actual := len(server.Requests())
	if actual != expected {
		t.Errorf("Expected %d requests, got %d", expected, actual)
	}
}

// AssertLastMessageContains checks if the last user message contains the text.
func AssertLastMessageContains(t *testing.T, server *Server, text string) {
	t.Helper()
	req := server.LastRequest()
	if req == nil {
		t.Error("No requests made")
		return
	}

	for i := len(req.Body.Messages) - 1; i >= 0; i-- {
		if req.Body.Messages[i].Role == "user" {
			if !containsIgnoreCase(req.Body.Messages[i].Content, text) {
				t.Errorf("Last user message does not contain %q", text)
			}
			return
		}
	}
	t.Error("No user messages found")
}

// AssertToolWasCalled checks if a specific tool was called.
func AssertToolWasCalled(t *testing.T, server *Server, toolName string) {
	t.Helper()
	for _, req := range server.Requests() {
		for _, msg := range req.Body.Messages {
			if msg.Role == "assistant" {
				for _, tc := range msg.ToolCalls {
					if tc.Function.Name == toolName {
						return
					}
				}
			}
		}
	}
	t.Errorf("Tool %q was not called", toolName)
}

// AssertToolWasNotCalled checks that a specific tool was not called.
func AssertToolWasNotCalled(t *testing.T, server *Server, toolName string) {
	t.Helper()
	for _, req := range server.Requests() {
		for _, msg := range req.Body.Messages {
			if msg.Role == "assistant" {
				for _, tc := range msg.ToolCalls {
					if tc.Function.Name == toolName {
						t.Errorf("Tool %q was called unexpectedly", toolName)
						return
					}
				}
			}
		}
	}
}

func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		findIgnoreCase(s, substr) >= 0)
}

func findIgnoreCase(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if equalFoldSlice(s[i:i+len(substr)], substr) {
			return i
		}
	}
	return -1
}

func equalFoldSlice(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
