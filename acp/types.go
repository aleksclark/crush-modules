// Package acp provides an Agent Communication Protocol (ACP) client for Crush.
//
// ACP is a REST protocol that lets agents discover and invoke other agents
// over HTTP, using Streamable HTTP (NDJSON) for streaming responses.
// This plugin exposes remote ACP agents as tools the LLM can call.
//
// The plugin is DISABLED by default. To enable it, configure at least one server
// in crush.json:
//
//	{
//	  "options": {
//	    "plugins": {
//	      "acp": {
//	        "servers": [
//	          {"name": "local", "url": "http://localhost:8000"}
//	        ]
//	      }
//	    }
//	  }
//	}
package acp

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

// IsTerminal returns true if the run status is a terminal state.
func (s RunStatus) IsTerminal() bool {
	switch s {
	case RunStatusCompleted, RunStatusFailed, RunStatusCancelled:
		return true
	}
	return false
}

// RunMode determines how a run is executed.
type RunMode string

const (
	RunModeSync   RunMode = "sync"
	RunModeAsync  RunMode = "async"
	RunModeStream RunMode = "stream"
)

// AgentManifest describes a remote ACP agent.
type AgentManifest struct {
	Name               string          `json:"name"`
	Description        string          `json:"description,omitempty"`
	InputContentTypes  []string        `json:"input_content_types,omitempty"`
	OutputContentTypes []string        `json:"output_content_types,omitempty"`
	Metadata           *AgentMetadata  `json:"metadata,omitempty"`
	Status             *AgentStatus    `json:"status,omitempty"`
}

// AgentMetadata contains optional discovery information about an agent.
type AgentMetadata struct {
	Documentation    string              `json:"documentation,omitempty"`
	License          string              `json:"license,omitempty"`
	Framework        string              `json:"framework,omitempty"`
	Capabilities     []AgentCapability   `json:"capabilities,omitempty"`
	Domains          []string            `json:"domains,omitempty"`
	Tags             []string            `json:"tags,omitempty"`
	NaturalLanguages []string            `json:"natural_languages,omitempty"`
	Author           *AgentAuthor        `json:"author,omitempty"`
}

// AgentCapability describes a specific capability of an agent.
type AgentCapability struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// AgentAuthor describes the author of an agent.
type AgentAuthor struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// AgentStatus contains runtime metrics about an agent.
type AgentStatus struct {
	AvgRunTokens      int     `json:"avg_run_tokens,omitempty"`
	AvgRunTimeSeconds float64 `json:"avg_run_time_seconds,omitempty"`
	SuccessRate       float64 `json:"success_rate,omitempty"`
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
	Name            string   `json:"name,omitempty"`
	ContentType     string   `json:"content_type"`
	Content         string   `json:"content,omitempty"`
	ContentEncoding string   `json:"content_encoding,omitempty"`
	ContentURL      string   `json:"content_url,omitempty"`
	Metadata        Metadata `json:"metadata,omitempty"`
}

// Metadata is a flexible metadata container for message parts.
type Metadata map[string]any

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

// RunCreateRequest is the body for POST /runs.
type RunCreateRequest struct {
	AgentName string    `json:"agent_name"`
	Input     []Message `json:"input"`
	SessionID string    `json:"session_id,omitempty"`
	Mode      RunMode   `json:"mode,omitempty"`
}

// RunResumeRequest is the body for POST /runs/{run_id}.
type RunResumeRequest struct {
	RunID       string       `json:"run_id"`
	AwaitResume *AwaitResume `json:"await_resume"`
	Mode        RunMode      `json:"mode,omitempty"`
}

// AgentsListResponse is the response from GET /agents.
type AgentsListResponse struct {
	Agents []AgentManifest `json:"agents"`
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

// Event is a discriminated union of all streaming event types.
type Event struct {
	Type    EventType    `json:"type"`
	Run     *Run         `json:"run,omitempty"`
	Message *Message     `json:"message,omitempty"`
	Part    *MessagePart `json:"part,omitempty"`
	Error   *ACPError    `json:"error,omitempty"`
	Generic any          `json:"generic,omitempty"`
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

// SessionMessageEvent is streamed during runs to provide real-time session
// state updates. Clients can collect these to reconstruct the full session
// after a crash without needing to call the export endpoint.
type SessionMessageEvent struct {
	EventType   string              `json:"event_type"`
	MessageID   string              `json:"message_id"`
	SessionID   string              `json:"session_id"`
	Role        string              `json:"role"`
	Content     string              `json:"content,omitempty"`
	ToolCalls   []SessionToolCall   `json:"tool_calls,omitempty"`
	ToolResults []SessionToolResult `json:"tool_results,omitempty"`
}

// SessionToolCall represents a tool invocation in a session message event.
type SessionToolCall struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Input    string `json:"input,omitempty"`
	Finished bool   `json:"finished"`
}

// SessionToolResult represents a tool result in a session message event.
type SessionToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
}
