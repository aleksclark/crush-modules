package a2a

import (
	"encoding/json"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/stretchr/testify/require"
)

func TestServerConfigIsEnabled(t *testing.T) {
	t.Parallel()

	enabled := true
	disabled := false

	require.True(t, ServerConfig{Enabled: nil}.IsEnabled())
	require.True(t, ServerConfig{Enabled: &enabled}.IsEnabled())
	require.False(t, ServerConfig{Enabled: &disabled}.IsEnabled())
}

func TestConfigJSON(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Servers: []ServerConfig{
			{Name: "local", URL: "http://localhost:8200"},
		},
		DefaultTimeoutSeconds: 60,
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	var decoded Config
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Len(t, decoded.Servers, 1)
	require.Equal(t, "local", decoded.Servers[0].Name)
	require.Equal(t, 60, decoded.DefaultTimeoutSeconds)
}

func TestA2AServerConfigJSON(t *testing.T) {
	t.Parallel()

	cfg := A2AServerConfig{
		Port:      8200,
		AgentName: "crush",
		Skills: []SkillConfig{
			{ID: "code", Name: "Code", Description: "Write code", Tags: []string{"coding"}},
		},
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	var decoded A2AServerConfig
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, 8200, decoded.Port)
	require.Len(t, decoded.Skills, 1)
	require.Equal(t, "code", decoded.Skills[0].ID)
}

func TestExtractText(t *testing.T) {
	t.Parallel()

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Hello"), a2a.NewTextPart("World"))
	require.Equal(t, "Hello\nWorld", ExtractText(msg))
}

func TestExtractTextNil(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", ExtractText(nil))
}

func TestExtractTextEmpty(t *testing.T) {
	t.Parallel()

	msg := a2a.NewMessage(a2a.MessageRoleUser)
	require.Equal(t, "", ExtractText(msg))
}

func TestExtractTextFromParts(t *testing.T) {
	t.Parallel()

	parts := a2a.ContentParts{a2a.NewTextPart("foo"), a2a.NewTextPart("bar")}
	require.Equal(t, "foo\nbar", ExtractTextFromParts(parts))
}

func TestExtractTextFromPartsEmpty(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", ExtractTextFromParts(nil))
}
