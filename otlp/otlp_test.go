package otlp

import (
	"context"
	"testing"
	"time"

	"github.com/charmbracelet/crush/plugin"
	"github.com/stretchr/testify/require"
)

func TestOTLPHookRegistration(t *testing.T) {
	t.Parallel()

	// Verify the hook is registered.
	hooks := plugin.RegisteredHooks()
	found := false
	for _, name := range hooks {
		if name == HookName {
			found = true
			break
		}
	}
	require.True(t, found, "OTLP hook should be registered")
}

func TestOTLPHookFactory(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp(
		plugin.WithPluginConfig(map[string]map[string]any{
			HookName: {
				"endpoint":     "http://localhost:4318",
				"service_name": "test-service",
				"insecure":     true,
			},
		}),
	)

	factory, ok := plugin.GetHookFactory(HookName)
	require.True(t, ok, "OTLP hook factory should exist")

	ctx := context.Background()
	hook, err := factory(ctx, app)
	require.NoError(t, err)
	require.NotNil(t, hook)
	require.Equal(t, HookName, hook.Name())
}

func TestOTLPHookDefaultConfig(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp()

	hook, err := NewOTLPHook(app, Config{})
	require.NoError(t, err)
	require.NotNil(t, hook)
	require.Equal(t, DefaultEndpoint, hook.cfg.Endpoint)
	require.Equal(t, DefaultServiceName, hook.cfg.ServiceName)
	require.Equal(t, DefaultContentLimit, hook.cfg.ContentLimit)
	require.Equal(t, DefaultToolInputLimit, hook.cfg.ToolInputLimit)
	require.Equal(t, DefaultToolResultLimit, hook.cfg.ToolResultLimit)
}

func TestOTLPHookCustomConfig(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Endpoint:        "http://custom:4318",
		ServiceName:     "custom-service",
		Insecure:        true,
		ContentLimit:    8000,
		ToolInputLimit:  10000,
		ToolResultLimit: 5000,
		Headers: map[string]string{
			"Authorization": "Bearer token",
		},
	}

	app := plugin.NewApp()
	hook, err := NewOTLPHook(app, cfg)
	require.NoError(t, err)
	require.NotNil(t, hook)
	require.Equal(t, "http://custom:4318", hook.cfg.Endpoint)
	require.Equal(t, "custom-service", hook.cfg.ServiceName)
	require.True(t, hook.cfg.Insecure)
	require.Equal(t, 8000, hook.cfg.ContentLimit)
	require.Equal(t, 10000, hook.cfg.ToolInputLimit)
	require.Equal(t, 5000, hook.cfg.ToolResultLimit)
	require.Equal(t, "Bearer token", hook.cfg.Headers["Authorization"])
}

func TestOTLPHookStartWithoutMessageSubscriber(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp()
	hook, err := NewOTLPHook(app, Config{
		Endpoint: "http://localhost:4318",
		Insecure: true,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start should return nil when no message subscriber is available.
	err = hook.Start(ctx)
	require.NoError(t, err)
}

func TestOTLPHookStopWithoutStart(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp()
	hook, err := NewOTLPHook(app, Config{})
	require.NoError(t, err)

	// Stop should be safe to call without Start.
	err = hook.Stop()
	require.NoError(t, err)
}

// mockMessageSubscriber implements plugin.MessageSubscriber for testing.
type mockMessageSubscriber struct {
	events chan plugin.MessageEvent
}

func newMockMessageSubscriber() *mockMessageSubscriber {
	return &mockMessageSubscriber{
		events: make(chan plugin.MessageEvent, 10),
	}
}

func (m *mockMessageSubscriber) SubscribeMessages(ctx context.Context) <-chan plugin.MessageEvent {
	out := make(chan plugin.MessageEvent, 10)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-m.events:
				if !ok {
					return
				}
				select {
				case out <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func (m *mockMessageSubscriber) Send(e plugin.MessageEvent) {
	m.events <- e
}

func (m *mockMessageSubscriber) Close() {
	close(m.events)
}

func TestTruncateString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		limit    int
		expected string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact limit", "hello", 5, "hello"},
		{"over limit", "hello world", 5, "hello..."},
		{"empty string", "", 10, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.limit)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeGitURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"https URL", "https://github.com/user/repo.git", "github.com/user/repo"},
		{"ssh URL", "git@github.com:user/repo.git", "github.com/user/repo"},
		{"http URL", "http://github.com/user/repo", "github.com/user/repo"},
		{"no git suffix", "https://github.com/user/repo", "github.com/user/repo"},
		{"already normalized", "github.com/user/repo", "github.com/user/repo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeGitURL(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestIsFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"absolute path", "/home/user/file.go", true},
		{"relative dot path", "./file.go", true},
		{"parent path", "../file.go", true},
		{"path with slash", "src/file.go", true},
		{"plain word", "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isFilePath(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestOTLPHookProjectInfo(t *testing.T) {
	t.Parallel()

	// Create a hook with a working directory.
	app := plugin.NewApp(
		plugin.WithWorkingDir("/home/user/myproject"),
	)

	hook, err := NewOTLPHook(app, Config{})
	require.NoError(t, err)
	require.NotNil(t, hook)
	require.Equal(t, "/home/user/myproject", hook.projectPath)
	require.Equal(t, "myproject", hook.projectName)
}

func TestOTLPHookProcessMessages(t *testing.T) {
	t.Parallel()

	mock := newMockMessageSubscriber()
	defer mock.Close()

	app := plugin.NewApp(
		plugin.WithMessageSubscriber(mock),
	)

	hook, err := NewOTLPHook(app, Config{
		Endpoint: "http://localhost:4318",
		Insecure: true,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start hook in background.
	errCh := make(chan error, 1)
	go func() {
		errCh <- hook.Start(ctx)
	}()

	// Give time for initialization.
	time.Sleep(50 * time.Millisecond)

	// Send a user message.
	mock.Send(plugin.MessageEvent{
		Type: plugin.MessageCreated,
		Message: plugin.Message{
			ID:        "msg-1",
			SessionID: "session-1",
			Role:      plugin.MessageRoleUser,
			Content:   "Hello, world!",
		},
	})

	// Send an assistant message with a tool call.
	mock.Send(plugin.MessageEvent{
		Type: plugin.MessageCreated,
		Message: plugin.Message{
			ID:        "msg-2",
			SessionID: "session-1",
			Role:      plugin.MessageRoleAssistant,
			Content:   "I'll help you with that.",
			ToolCalls: []plugin.ToolCallInfo{
				{
					ID:       "tc-1",
					Name:     "ping",
					Input:    "{}",
					Finished: false,
				},
			},
		},
	})

	// Update with finished tool call.
	mock.Send(plugin.MessageEvent{
		Type: plugin.MessageUpdated,
		Message: plugin.Message{
			ID:        "msg-2",
			SessionID: "session-1",
			Role:      plugin.MessageRoleAssistant,
			ToolCalls: []plugin.ToolCallInfo{
				{
					ID:       "tc-1",
					Name:     "ping",
					Input:    "{}",
					Finished: true,
				},
			},
		},
	})

	// Send tool result.
	mock.Send(plugin.MessageEvent{
		Type: plugin.MessageCreated,
		Message: plugin.Message{
			ID:        "msg-3",
			SessionID: "session-1",
			Role:      plugin.MessageRoleTool,
			ToolResults: []plugin.ToolResultInfo{
				{
					ToolCallID: "tc-1",
					Name:       "ping",
					Content:    "pong",
					IsError:    false,
				},
			},
		},
	})

	// Wait for context to finish.
	<-ctx.Done()

	// Verify hook stopped without error.
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("hook did not stop in time")
	}
}
