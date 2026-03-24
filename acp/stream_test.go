package acp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseStreamBasic(t *testing.T) {
	t.Parallel()

	stream := `{"type":"run.created","run":{"agent_name":"echo","run_id":"r1","status":"created","output":[],"created_at":"2025-01-01T00:00:00Z"}}
{"type":"message.part","part":{"content_type":"text/plain","content":"Hello"}}
{"type":"message.part","part":{"content_type":"text/plain","content":" World"}}
{"type":"run.completed","run":{"agent_name":"echo","run_id":"r1","status":"completed","output":[],"created_at":"2025-01-01T00:00:00Z"}}
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

func TestParseStreamSkipsEmptyLines(t *testing.T) {
	t.Parallel()

	stream := `{"type":"message.part","part":{"content_type":"text/plain","content":"ok"}}

{"type":"run.completed","run":{"agent_name":"echo","run_id":"r1","status":"completed","output":[],"created_at":"2025-01-01T00:00:00Z"}}
`

	events := collectEvents(t, stream)
	require.Len(t, events, 2)
	require.Equal(t, EventMessagePart, events[0].Type)
	require.Equal(t, EventRunCompleted, events[1].Type)
}

func TestParseStreamInvalidJSON(t *testing.T) {
	t.Parallel()

	stream := "not-valid-json\n"

	events := collectEvents(t, stream)
	require.Len(t, events, 1)
	require.Equal(t, EventError, events[0].Type)
	require.Contains(t, events[0].Error.Message, "failed to parse event")
}

func TestParseStreamEmpty(t *testing.T) {
	t.Parallel()

	events := collectEvents(t, "")
	require.Empty(t, events)
}

func TestParseStreamAwaitingEvent(t *testing.T) {
	t.Parallel()

	stream := `{"type":"run.awaiting","run":{"agent_name":"approval","run_id":"r2","status":"awaiting","output":[],"await_request":{"message":{"role":"agent","parts":[{"content_type":"text/plain","content":"Do you approve?"}]}},"created_at":"2025-01-01T00:00:00Z"}}
`

	events := collectEvents(t, stream)
	require.Len(t, events, 1)
	require.Equal(t, EventRunAwaiting, events[0].Type)
	require.NotNil(t, events[0].Run)
	require.Equal(t, RunStatusAwaiting, events[0].Run.Status)
	require.NotNil(t, events[0].Run.AwaitRequest)
	require.Equal(t, "Do you approve?", events[0].Run.AwaitRequest.Message.Parts[0].Content)
}

func TestParseStreamErrorEvent(t *testing.T) {
	t.Parallel()

	stream := `{"type":"error","error":{"code":500,"message":"internal error"}}
`

	events := collectEvents(t, stream)
	require.Len(t, events, 1)
	require.Equal(t, EventError, events[0].Type)
	require.Equal(t, "internal error", events[0].Error.Message)
}

func TestParseStreamDetectsSSE(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stream string
	}{
		{"data prefix with space", "data: {\"type\":\"message.part\"}\n"},
		{"data prefix no space", "data:{\"type\":\"message.part\"}\n"},
		{"event line", "event: message\n"},
		{"done marker", "[DONE]\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			events := collectEvents(t, tt.stream)
			require.Len(t, events, 1)
			require.Equal(t, EventError, events[0].Type)
			require.Contains(t, events[0].Error.Message, "server sent SSE-formatted data instead of NDJSON")
			require.Contains(t, events[0].Error.Message, "application/x-ndjson")
		})
	}
}

func TestLooksLikeSSE(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
		want bool
	}{
		{"plain json", `{"type":"error"}`, false},
		{"data prefix with space", `data: {"type":"error"}`, true},
		{"data prefix no space", `data:{"type":"error"}`, true},
		{"event line", "event: message", true},
		{"id line", "id: 123", true},
		{"retry line", "retry: 3000", true},
		{"sse comment", ": heartbeat", true},
		{"done marker", "[DONE]", true},
		{"random garbage", "not-json-at-all", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := looksLikeSSE(tt.line)
			require.Equal(t, tt.want, got)
		})
	}
}

func collectEvents(t *testing.T, stream string) []Event {
	t.Helper()
	ch := ParseStream(strings.NewReader(stream))
	var events []Event
	for e := range ch {
		events = append(events, e)
	}
	return events
}
