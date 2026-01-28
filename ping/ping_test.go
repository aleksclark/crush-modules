package ping

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// TestPingToolReturnsPong verifies that when the ping tool is invoked, it responds with "pong".
func TestPingToolReturnsPong(t *testing.T) {
	t.Parallel()

	tool := NewPingTool()

	// Verify tool info.
	require.Equal(t, ToolName, tool.Info().Name)
	require.Contains(t, tool.Info().Description, "pong")

	// Invoke the tool with an empty input (no params required).
	call := fantasy.ToolCall{
		ID:    "test-call-1",
		Name:  ToolName,
		Input: "{}",
	}

	resp, err := tool.Run(context.Background(), call)
	require.NoError(t, err)

	// Verify the response is "pong".
	require.Equal(t, "pong", resp.Content)
}

// TestPingToolMultipleInvocations verifies the tool can be called multiple times.
func TestPingToolMultipleInvocations(t *testing.T) {
	t.Parallel()

	tool := NewPingTool()

	for i := 0; i < 3; i++ {
		call := fantasy.ToolCall{
			ID:    "test-call",
			Name:  ToolName,
			Input: "{}",
		}

		resp, err := tool.Run(context.Background(), call)
		require.NoError(t, err)
		require.Equal(t, "pong", resp.Content)
	}
}
