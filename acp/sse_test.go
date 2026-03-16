package acp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSSEStreamBasic(t *testing.T) {
	t.Parallel()

	stream := `data: {"type":"run.created","run":{"agent_name":"echo","run_id":"r1","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}

data: {"type":"message.part","part":{"content_type":"text/plain","content":"Hello"}}

data: {"type":"message.part","part":{"content_type":"text/plain","content":" World"}}

data: {"type":"run.completed","run":{"agent_name":"echo","run_id":"r1","status":"completed","output":[],"created_at":"2025-01-01T00:00:00Z"}}

`

	events := collectEvents(t, stream)
	require.Len(t, events, 4)
	require.Equal(t, EventRunCreated, events[0].Type)
	require.Equal(t, EventMessagePart, events[1].Type)
	require.Equal(t, "Hello", events[1].Part.Content)
	require.Equal(t, EventMessagePart, events[2].Type)
	require.Equal(t, " World", events[2].Part.Content)
	require.Equal(t, EventRunCompleted, events[3].Type)
}

func TestParseSSEStreamMultilineData(t *testing.T) {
	t.Parallel()

	stream := `data: {"type":"message.part",
data: "part":{"content_type":"text/plain","content":"multi"}}

`

	events := collectEvents(t, stream)
	require.Len(t, events, 1)
	require.Equal(t, EventMessagePart, events[0].Type)
	require.Equal(t, "multi", events[0].Part.Content)
}

func TestParseSSEStreamIgnoresComments(t *testing.T) {
	t.Parallel()

	stream := `: this is a comment
data: {"type":"message.part","part":{"content_type":"text/plain","content":"ok"}}

`

	events := collectEvents(t, stream)
	require.Len(t, events, 1)
	require.Equal(t, "ok", events[0].Part.Content)
}

func TestParseSSEStreamInvalidJSON(t *testing.T) {
	t.Parallel()

	stream := `data: not-valid-json

`

	events := collectEvents(t, stream)
	require.Len(t, events, 1)
	require.Equal(t, EventError, events[0].Type)
	require.Contains(t, events[0].Error.Message, "failed to parse SSE event")
}

func TestParseSSEStreamEmpty(t *testing.T) {
	t.Parallel()

	events := collectEvents(t, "")
	require.Empty(t, events)
}

func TestParseSSEStreamAwaitingEvent(t *testing.T) {
	t.Parallel()

	stream := `data: {"type":"run.awaiting","run":{"agent_name":"approval","run_id":"r2","status":"awaiting","output":[],"await_request":{"message":{"role":"agent","parts":[{"content_type":"text/plain","content":"Do you approve?"}]}},"created_at":"2025-01-01T00:00:00Z"}}

`

	events := collectEvents(t, stream)
	require.Len(t, events, 1)
	require.Equal(t, EventRunAwaiting, events[0].Type)
	require.NotNil(t, events[0].Run)
	require.Equal(t, RunStatusAwaiting, events[0].Run.Status)
	require.NotNil(t, events[0].Run.AwaitRequest)
	require.Equal(t, "Do you approve?", events[0].Run.AwaitRequest.Message.Parts[0].Content)
}

func TestParseSSEStreamErrorEvent(t *testing.T) {
	t.Parallel()

	stream := `data: {"type":"error","error":{"code":500,"message":"internal error"}}

`

	events := collectEvents(t, stream)
	require.Len(t, events, 1)
	require.Equal(t, EventError, events[0].Type)
	require.Equal(t, "internal error", events[0].Error.Message)
}

func collectEvents(t *testing.T, stream string) []Event {
	t.Helper()
	ch := ParseSSEStream(strings.NewReader(stream))
	var events []Event
	for e := range ch {
		events = append(events, e)
	}
	return events
}
