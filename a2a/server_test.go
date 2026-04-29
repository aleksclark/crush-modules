package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	a2acore "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/charmbracelet/crush/plugin"
	"github.com/stretchr/testify/require"
)

func TestNewServerHookDefaults(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp(plugin.WithLogger(nil))
	hook, err := NewServerHook(app, A2AServerConfig{})
	require.NoError(t, err)
	require.NotNil(t, hook)
	require.Equal(t, DefaultPort, hook.cfg.Port)
	require.Equal(t, DefaultAgentName, hook.cfg.AgentName)
	require.Equal(t, HookName, hook.Name())
}

func TestNewServerHookCustomConfig(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp(plugin.WithLogger(nil))
	hook, err := NewServerHook(app, A2AServerConfig{
		Port:        9999,
		AgentName:   "custom-agent",
		Description: "Custom description",
		Skills: []SkillConfig{
			{ID: "test", Name: "Test Skill", Description: "A test skill", Tags: []string{"test"}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 9999, hook.cfg.Port)
	require.Equal(t, "custom-agent", hook.cfg.AgentName)
	require.Len(t, hook.card.Skills, 1)
	require.Equal(t, "test", hook.card.Skills[0].ID)
}

func TestServerStartAndServeAgentCard(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	app := plugin.NewApp(plugin.WithLogger(nil))
	hook, err := NewServerHook(app, A2AServerConfig{Port: port})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- hook.Start(ctx) }()

	select {
	case <-hook.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("server did not become ready")
	}

	// Verify agent card at well-known path.
	resp, err := http.Get(fmt.Sprintf("http://%s/.well-known/agent-card.json", hook.Addr()))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	var card a2acore.AgentCard
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&card))
	require.Equal(t, "crush", card.Name)
	require.Equal(t, "1.0.0", card.Version)
	require.NotEmpty(t, card.SupportedInterfaces)
	require.NotEmpty(t, card.Skills)
	require.True(t, card.Capabilities.Streaming)
	require.Equal(t, []string{"text/plain"}, card.DefaultInputModes)
	require.Equal(t, []string{"text/plain"}, card.DefaultOutputModes)

	cancel()
	require.NoError(t, <-errCh)
}

func TestServerAgentCardSkillsDefault(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp(plugin.WithLogger(nil))
	hook, err := NewServerHook(app, A2AServerConfig{})
	require.NoError(t, err)
	require.Len(t, hook.card.Skills, 2)
	require.Equal(t, "code", hook.card.Skills[0].ID)
	require.Equal(t, "tools", hook.card.Skills[1].ID)
}

func TestServerConfigApplyEnv(t *testing.T) {
	t.Run("overrides all fields", func(t *testing.T) {
		t.Setenv("CRUSH_A2A_PORT", "9999")
		t.Setenv("CRUSH_A2A_AGENT_NAME", "my-agent")
		t.Setenv("CRUSH_A2A_DESCRIPTION", "custom desc")

		cfg := A2AServerConfig{Port: 8200, AgentName: "crush", Description: "default"}
		cfg.applyEnv()

		require.Equal(t, 9999, cfg.Port)
		require.Equal(t, "my-agent", cfg.AgentName)
		require.Equal(t, "custom desc", cfg.Description)
	})

	t.Run("leaves unset fields unchanged", func(t *testing.T) {
		cfg := A2AServerConfig{Port: 8200, AgentName: "crush", Description: "default"}
		cfg.applyEnv()

		require.Equal(t, 8200, cfg.Port)
		require.Equal(t, "crush", cfg.AgentName)
		require.Equal(t, "default", cfg.Description)
	})

	t.Run("ignores invalid port", func(t *testing.T) {
		t.Setenv("CRUSH_A2A_PORT", "notanumber")

		cfg := A2AServerConfig{Port: 8200}
		cfg.applyEnv()

		require.Equal(t, 8200, cfg.Port)
	})

	t.Run("ignores zero port", func(t *testing.T) {
		t.Setenv("CRUSH_A2A_PORT", "0")

		cfg := A2AServerConfig{Port: 8200}
		cfg.applyEnv()

		require.Equal(t, 8200, cfg.Port)
	})
}

func TestServerCurrentTaskIDEmpty(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp(plugin.WithLogger(nil))
	hook, err := NewServerHook(app, A2AServerConfig{})
	require.NoError(t, err)
	require.Equal(t, a2acore.TaskID(""), hook.CurrentTaskID())
}

func TestServerArtifactStore(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp(plugin.WithLogger(nil))
	hook, err := NewServerHook(app, A2AServerConfig{})
	require.NoError(t, err)
	require.NotNil(t, hook.ArtifactStore())
}
