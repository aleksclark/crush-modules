package mockllm

import (
	"encoding/json"
	"strings"
)

// Response builder functions that create ResponseFunc for common patterns.

// TextResponse creates a simple text response.
func TextResponse(content string) func(req *ChatRequest) *ChatResponse {
	return func(req *ChatRequest) *ChatResponse {
		resp := NewResponse(req.Model)
		resp.Choices = []Choice{{
			Index: 0,
			Message: Message{
				Role:    "assistant",
				Content: content,
			},
			FinishReason: "stop",
		}}
		return resp
	}
}

// ToolCallResponse creates a response that invokes a tool.
func ToolCallResponse(toolName string, arguments any) func(req *ChatRequest) *ChatResponse {
	return func(req *ChatRequest) *ChatResponse {
		args := "{}"
		switch v := arguments.(type) {
		case string:
			args = v
		case []byte:
			args = string(v)
		default:
			if b, err := json.Marshal(v); err == nil {
				args = string(b)
			}
		}

		resp := NewResponse(req.Model)
		resp.Choices = []Choice{{
			Index: 0,
			Message: Message{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:   "call_" + randomID(),
					Type: "function",
					Function: FunctionCall{
						Name:      toolName,
						Arguments: args,
					},
				}},
			},
			FinishReason: "tool_calls",
		}}
		return resp
	}
}

// MultiToolCallResponse creates a response that invokes multiple tools.
func MultiToolCallResponse(calls ...ToolCallSpec) func(req *ChatRequest) *ChatResponse {
	return func(req *ChatRequest) *ChatResponse {
		var toolCalls []ToolCall
		for _, call := range calls {
			args := "{}"
			switch v := call.Arguments.(type) {
			case string:
				args = v
			case []byte:
				args = string(v)
			default:
				if b, err := json.Marshal(v); err == nil {
					args = string(b)
				}
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   "call_" + randomID(),
				Type: "function",
				Function: FunctionCall{
					Name:      call.Name,
					Arguments: args,
				},
			})
		}

		resp := NewResponse(req.Model)
		resp.Choices = []Choice{{
			Index: 0,
			Message: Message{
				Role:      "assistant",
				ToolCalls: toolCalls,
			},
			FinishReason: "tool_calls",
		}}
		return resp
	}
}

// ToolCallSpec defines a tool call for MultiToolCallResponse.
type ToolCallSpec struct {
	Name      string
	Arguments any
}

// TextAndToolResponse creates a response with both text and a tool call.
func TextAndToolResponse(content, toolName string, arguments any) func(req *ChatRequest) *ChatResponse {
	return func(req *ChatRequest) *ChatResponse {
		args := "{}"
		switch v := arguments.(type) {
		case string:
			args = v
		case []byte:
			args = string(v)
		default:
			if b, err := json.Marshal(v); err == nil {
				args = string(b)
			}
		}

		resp := NewResponse(req.Model)
		resp.Choices = []Choice{{
			Index: 0,
			Message: Message{
				Role:    "assistant",
				Content: content,
				ToolCalls: []ToolCall{{
					ID:   "call_" + randomID(),
					Type: "function",
					Function: FunctionCall{
						Name:      toolName,
						Arguments: args,
					},
				}},
			},
			FinishReason: "tool_calls",
		}}
		return resp
	}
}

// ErrorResponse creates a response with an error message.
func ErrorResponse(errorMessage string) func(req *ChatRequest) *ChatResponse {
	return func(req *ChatRequest) *ChatResponse {
		resp := NewResponse(req.Model)
		resp.Choices = []Choice{{
			Index: 0,
			Message: Message{
				Role:    "assistant",
				Content: "Error: " + errorMessage,
			},
			FinishReason: "stop",
		}}
		return resp
	}
}

// EmptyResponse creates a response with no content (edge case testing).
func EmptyResponse() func(req *ChatRequest) *ChatResponse {
	return func(req *ChatRequest) *ChatResponse {
		resp := NewResponse(req.Model)
		resp.Choices = []Choice{{
			Index:        0,
			Message:      Message{Role: "assistant"},
			FinishReason: "stop",
		}}
		return resp
	}
}

// EchoResponse creates a response that echoes the last user message.
func EchoResponse(prefix string) func(req *ChatRequest) *ChatResponse {
	return func(req *ChatRequest) *ChatResponse {
		content := prefix
		// Find last user message.
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				content += req.Messages[i].Content
				break
			}
		}

		resp := NewResponse(req.Model)
		resp.Choices = []Choice{{
			Index: 0,
			Message: Message{
				Role:    "assistant",
				Content: content,
			},
			FinishReason: "stop",
		}}
		return resp
	}
}

// Matcher functions for conditional responses.

// MessageContains returns true if the last user message contains the text.
func MessageContains(text string) MatchFunc {
	return func(req ChatRequest) bool {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				return strings.Contains(strings.ToLower(req.Messages[i].Content), strings.ToLower(text))
			}
		}
		return false
	}
}

// MessageEquals returns true if the last user message equals the text exactly.
func MessageEquals(text string) MatchFunc {
	return func(req ChatRequest) bool {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				return req.Messages[i].Content == text
			}
		}
		return false
	}
}

// HasToolResult returns true if there's a tool result message with the given name.
func HasToolResult(toolName string) MatchFunc {
	return func(req ChatRequest) bool {
		for _, msg := range req.Messages {
			if msg.Role == "tool" && msg.Name == toolName {
				return true
			}
		}
		return false
	}
}

// HasToolCall returns true if any assistant message contains a tool call with the given name.
func HasToolCall(toolName string) MatchFunc {
	return func(req ChatRequest) bool {
		for _, msg := range req.Messages {
			if msg.Role == "assistant" {
				for _, tc := range msg.ToolCalls {
					if tc.Function.Name == toolName {
						return true
					}
				}
			}
		}
		return false
	}
}

// HasSystemPrompt returns true if the request has a system message.
func HasSystemPrompt() MatchFunc {
	return func(req ChatRequest) bool {
		for _, msg := range req.Messages {
			if msg.Role == "system" {
				return true
			}
		}
		return false
	}
}

// SystemPromptContains returns true if the system prompt contains the text.
func SystemPromptContains(text string) MatchFunc {
	return func(req ChatRequest) bool {
		for _, msg := range req.Messages {
			if msg.Role == "system" && strings.Contains(msg.Content, text) {
				return true
			}
		}
		return false
	}
}

// MessageCount returns true if the request has exactly n messages.
func MessageCount(n int) MatchFunc {
	return func(req ChatRequest) bool {
		return len(req.Messages) == n
	}
}

// Always returns true for any request.
func Always() MatchFunc {
	return func(req ChatRequest) bool {
		return true
	}
}

// Never returns false for any request.
func Never() MatchFunc {
	return func(req ChatRequest) bool {
		return false
	}
}

// And combines matchers with AND logic.
func And(matchers ...MatchFunc) MatchFunc {
	return func(req ChatRequest) bool {
		for _, m := range matchers {
			if !m(req) {
				return false
			}
		}
		return true
	}
}

// Or combines matchers with OR logic.
func Or(matchers ...MatchFunc) MatchFunc {
	return func(req ChatRequest) bool {
		for _, m := range matchers {
			if m(req) {
				return true
			}
		}
		return false
	}
}

// Not negates a matcher.
func Not(m MatchFunc) MatchFunc {
	return func(req ChatRequest) bool {
		return !m(req)
	}
}
