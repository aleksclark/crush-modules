package sdk

import "time"

// RunStatus represents the state of an ACP run.
type RunStatus string

const (
	RunStatusCreated    RunStatus = "created"
	RunStatusInProgress RunStatus = "in-progress"
	RunStatusAwaiting   RunStatus = "awaiting"
	RunStatusCompleted  RunStatus = "completed"
	RunStatusFailed     RunStatus = "failed"
	RunStatusCancelling RunStatus = "cancelling"
	RunStatusCancelled  RunStatus = "cancelled"
)

// IsTerminal returns true if the status represents a final state.
func (s RunStatus) IsTerminal() bool {
	switch s {
	case RunStatusCompleted, RunStatusFailed, RunStatusCancelled:
		return true
	}
	return false
}

// RunMode determines how a run is executed by the server.
type RunMode string

const (
	RunModeSync   RunMode = "sync"
	RunModeAsync  RunMode = "async"
	RunModeStream RunMode = "stream"
)

// AgentManifest describes a remote ACP agent's capabilities.
type AgentManifest struct {
	Name               string         `json:"name"`
	Description        string         `json:"description,omitempty"`
	InputContentTypes  []string       `json:"input_content_types,omitempty"`
	OutputContentTypes []string       `json:"output_content_types,omitempty"`
	Metadata           *AgentMetadata `json:"metadata,omitempty"`
}

// AgentMetadata contains optional discovery information.
type AgentMetadata struct {
	Documentation string            `json:"documentation,omitempty"`
	Framework     string            `json:"framework,omitempty"`
	Capabilities  []AgentCapability `json:"capabilities,omitempty"`
	Tags          []string          `json:"tags,omitempty"`
}

// AgentCapability describes a specific agent capability.
type AgentCapability struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Message is the core communication unit in ACP.
type Message struct {
	Role        string        `json:"role"`
	Parts       []MessagePart `json:"parts"`
	CreatedAt   *time.Time    `json:"created_at,omitempty"`
	CompletedAt *time.Time    `json:"completed_at,omitempty"`
}

// MessagePart is a single typed content unit within a message.
type MessagePart struct {
	Name            string         `json:"name,omitempty"`
	ContentType     string         `json:"content_type"`
	Content         string         `json:"content,omitempty"`
	ContentEncoding string         `json:"content_encoding,omitempty"`
	ContentURL      string         `json:"content_url,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

// Run represents an ACP agent run.
type Run struct {
	AgentName    string        `json:"agent_name"`
	RunID        string        `json:"run_id"`
	SessionID    string        `json:"session_id,omitempty"`
	Status       RunStatus     `json:"status"`
	Output       []Message     `json:"output"`
	AwaitRequest *AwaitRequest `json:"await_request,omitempty"`
	Error        *ACPError     `json:"error,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	FinishedAt   *time.Time    `json:"finished_at,omitempty"`
}

// AwaitRequest represents an agent's request for external input.
type AwaitRequest struct {
	Message *Message `json:"message,omitempty"`
}

// AwaitResume provides input to resume an awaiting run.
type AwaitResume struct {
	Message *Message `json:"message,omitempty"`
}

// ACPError represents an error returned by the ACP server.
type ACPError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *ACPError) Error() string {
	return e.Message
}

// EventType identifies the kind of streaming event.
type EventType string

const (
	EventRunCreated       EventType = "run.created"
	EventRunInProgress    EventType = "run.in-progress"
	EventRunAwaiting      EventType = "run.awaiting"
	EventRunCompleted     EventType = "run.completed"
	EventRunFailed        EventType = "run.failed"
	EventRunCancelled     EventType = "run.cancelled"
	EventMessageCreated   EventType = "message.created"
	EventMessagePart      EventType = "message.part"
	EventMessageCompleted EventType = "message.completed"
	EventSessionMessage   EventType = "session.message"
	EventSessionSnapshot  EventType = "session.snapshot"
	EventError            EventType = "error"
	EventGeneric          EventType = "generic"
)

// Event is a streaming event from the ACP server.
type Event struct {
	Type    EventType    `json:"type"`
	Run     *Run         `json:"run,omitempty"`
	Message *Message     `json:"message,omitempty"`
	Part    *MessagePart `json:"part,omitempty"`
	Error   *ACPError    `json:"error,omitempty"`
	Generic any          `json:"generic,omitempty"`
}

// SessionData contains session metadata.
type SessionData struct {
	ID               string  `json:"id"`
	Title            string  `json:"title"`
	SummaryMessageID string  `json:"summary_message_id,omitempty"`
	MessageCount     int64   `json:"message_count"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	Cost             float64 `json:"cost"`
	CreatedAt        int64   `json:"created_at"`
	UpdatedAt        int64   `json:"updated_at"`
}

// SessionMessage represents a single message in a session's conversation history.
// Parts is the raw JSON-encoded internal Crush message format — treat it as
// opaque for round-tripping through export/import.
type SessionMessage struct {
	ID               string `json:"id"`
	SessionID        string `json:"session_id"`
	Role             string `json:"role"`
	Parts            string `json:"parts"`
	Model            string `json:"model,omitempty"`
	Provider         string `json:"provider,omitempty"`
	IsSummaryMessage bool   `json:"is_summary_message,omitempty"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

// SessionSnapshot is a portable representation of a complete session
// including all conversation history. It can be serialized to JSON and
// transferred between Crush instances.
type SessionSnapshot struct {
	Version  int              `json:"version"`
	Session  SessionData      `json:"session"`
	Messages []SessionMessage `json:"messages"`
}

// --- Request types (internal) ---

type runCreateRequest struct {
	AgentName string    `json:"agent_name"`
	Input     []Message `json:"input"`
	SessionID string    `json:"session_id,omitempty"`
	Mode      RunMode   `json:"mode,omitempty"`
}

type agentsListResponse struct {
	Agents []AgentManifest `json:"agents"`
}

type importResponse struct {
	SessionID    string `json:"session_id"`
	MessageCount int    `json:"message_count"`
	Status       string `json:"status"`
}

// --- Helpers ---

// NewUserMessage creates a simple text message with the "user" role.
func NewUserMessage(text string) Message {
	return Message{
		Role: "user",
		Parts: []MessagePart{
			{ContentType: "text/plain", Content: text},
		},
	}
}

// NewAgentMessage creates a simple text message with the "agent" role.
func NewAgentMessage(text string) Message {
	return Message{
		Role: "agent",
		Parts: []MessagePart{
			{ContentType: "text/plain", Content: text},
		},
	}
}

// TextContent extracts all text/plain content from a slice of messages.
func TextContent(messages []Message) string {
	var result string
	for _, msg := range messages {
		for _, part := range msg.Parts {
			if part.ContentType == "text/plain" || part.ContentType == "" {
				if result != "" {
					result += "\n"
				}
				result += part.Content
			}
		}
	}
	return result
}
