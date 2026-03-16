package acp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunStatusIsTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status   RunStatus
		terminal bool
	}{
		{RunStatusCreated, false},
		{RunStatusInProgress, false},
		{RunStatusAwaiting, false},
		{RunStatusCancelling, false},
		{RunStatusCompleted, true},
		{RunStatusFailed, true},
		{RunStatusCancelled, true},
	}

	for _, tt := range tests {
		require.Equal(t, tt.terminal, tt.status.IsTerminal(), "status: %s", tt.status)
	}
}

func TestMessageRoundTrip(t *testing.T) {
	t.Parallel()

	msg := NewUserMessage("hello world")
	require.Equal(t, "user", msg.Role)
	require.Len(t, msg.Parts, 1)
	require.Equal(t, "text/plain", msg.Parts[0].ContentType)
	require.Equal(t, "hello world", msg.Parts[0].Content)

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded Message
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, msg.Role, decoded.Role)
	require.Equal(t, msg.Parts[0].Content, decoded.Parts[0].Content)
}

func TestAgentMessageRoundTrip(t *testing.T) {
	t.Parallel()

	msg := NewAgentMessage("I can help with that")
	require.Equal(t, "agent", msg.Role)
	require.Equal(t, "I can help with that", msg.Parts[0].Content)
}

func TestTextContent(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{
			Role: "agent",
			Parts: []MessagePart{
				{ContentType: "text/plain", Content: "Hello"},
				{ContentType: "image/png", Content: "binary"},
				{ContentType: "text/plain", Content: "World"},
			},
		},
		{
			Role: "agent",
			Parts: []MessagePart{
				{ContentType: "text/plain", Content: "!"},
			},
		},
	}

	result := TextContent(messages)
	require.Equal(t, "Hello\nWorld\n!", result)
}

func TestTextContentEmpty(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", TextContent(nil))
	require.Equal(t, "", TextContent([]Message{}))
	require.Equal(t, "", TextContent([]Message{{Role: "agent", Parts: []MessagePart{
		{ContentType: "image/png", Content: "binary"},
	}}}))
}

func TestRunCreateRequestJSON(t *testing.T) {
	t.Parallel()

	req := RunCreateRequest{
		AgentName: "summarizer",
		Input:     []Message{NewUserMessage("Summarize this")},
		Mode:      RunModeSync,
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "summarizer", decoded["agent_name"])
	require.Equal(t, "sync", decoded["mode"])
}

func TestRunJSON(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	run := Run{
		AgentName: "test-agent",
		RunID:     "run-123",
		Status:    RunStatusCompleted,
		Output:    []Message{NewAgentMessage("done")},
		CreatedAt: now,
	}

	data, err := json.Marshal(run)
	require.NoError(t, err)

	var decoded Run
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "test-agent", decoded.AgentName)
	require.Equal(t, RunStatusCompleted, decoded.Status)
	require.Equal(t, "done", decoded.Output[0].Parts[0].Content)
}

func TestACPErrorImplementsError(t *testing.T) {
	t.Parallel()

	err := &ACPError{Code: 404, Message: "agent not found"}
	require.Equal(t, "agent not found", err.Error())
}

func TestRunResumeRequestJSON(t *testing.T) {
	t.Parallel()

	req := RunResumeRequest{
		RunID: "run-456",
		AwaitResume: &AwaitResume{
			Message: &Message{
				Role:  "user",
				Parts: []MessagePart{{ContentType: "text/plain", Content: "yes"}},
			},
		},
		Mode: RunModeStream,
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var decoded RunResumeRequest
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "run-456", decoded.RunID)
	require.Equal(t, RunModeStream, decoded.Mode)
	require.Equal(t, "yes", decoded.AwaitResume.Message.Parts[0].Content)
}

func TestMessagePartArtifact(t *testing.T) {
	t.Parallel()

	part := MessagePart{
		Name:        "/report.pdf",
		ContentType: "application/pdf",
		ContentURL:  "https://example.com/report.pdf",
	}

	data, err := json.Marshal(part)
	require.NoError(t, err)

	var decoded MessagePart
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "/report.pdf", decoded.Name)
	require.Equal(t, "https://example.com/report.pdf", decoded.ContentURL)
}
