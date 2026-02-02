package mockllm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServerBasicTextResponse(t *testing.T) {
	t.Parallel()

	server := NewServer()
	server.OnAny(TextResponse("Hello, world!"))
	url := server.Start(t)

	resp := sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})

	require.Len(t, resp.Choices, 1)
	require.Equal(t, "Hello, world!", resp.Choices[0].Message.Content)
	require.Equal(t, "stop", resp.Choices[0].FinishReason)
}

func TestServerToolCallResponse(t *testing.T) {
	t.Parallel()

	server := NewServer()
	server.OnAny(ToolCallResponse("ping", map[string]any{"echo": true}))
	url := server.Start(t)

	resp := sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "call ping"}},
	})

	require.Len(t, resp.Choices, 1)
	require.Len(t, resp.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "ping", resp.Choices[0].Message.ToolCalls[0].Function.Name)
	require.Equal(t, "tool_calls", resp.Choices[0].FinishReason)
}

func TestServerMessageContainsMatcher(t *testing.T) {
	t.Parallel()

	server := NewServer()
	server.OnMessage("hello", TextResponse("Hi there!"))
	server.OnMessage("goodbye", TextResponse("See you!"))
	server.Default(TextResponse("I don't understand"))
	url := server.Start(t)

	// Test "hello" match.
	resp := sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "say hello"}},
	})
	require.Equal(t, "Hi there!", resp.Choices[0].Message.Content)

	// Test "goodbye" match.
	resp = sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "goodbye friend"}},
	})
	require.Equal(t, "See you!", resp.Choices[0].Message.Content)

	// Test default.
	resp = sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "something else"}},
	})
	require.Equal(t, "I don't understand", resp.Choices[0].Message.Content)
}

func TestServerSequence(t *testing.T) {
	t.Parallel()

	server := NewServer()
	server.Sequence(
		TextResponse("First response"),
		TextResponse("Second response"),
		TextResponse("Third response"),
	)
	url := server.Start(t)

	// First call.
	resp := sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "1"}},
	})
	require.Equal(t, "First response", resp.Choices[0].Message.Content)

	// Second call.
	resp = sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "2"}},
	})
	require.Equal(t, "Second response", resp.Choices[0].Message.Content)

	// Third call.
	resp = sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "3"}},
	})
	require.Equal(t, "Third response", resp.Choices[0].Message.Content)
}

func TestServerRequestLogging(t *testing.T) {
	t.Parallel()

	server := NewServer()
	server.OnAny(TextResponse("ok"))
	url := server.Start(t)

	// Make requests.
	sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "first"}},
	})
	sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "second"}},
	})

	// Check request log.
	requests := server.Requests()
	require.Len(t, requests, 2)
	require.Equal(t, "first", requests[0].Body.Messages[0].Content)
	require.Equal(t, "second", requests[1].Body.Messages[0].Content)

	// Check last request helper.
	last := server.LastRequest()
	require.NotNil(t, last)
	require.Equal(t, "second", last.Body.Messages[0].Content)
}

func TestServerToolResultMatcher(t *testing.T) {
	t.Parallel()

	server := NewServer()
	server.OnToolResult("ping", TextResponse("Pong received!"))
	server.Default(TextResponse("No tool result"))
	url := server.Start(t)

	// Without tool result.
	resp := sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	require.Equal(t, "No tool result", resp.Choices[0].Message.Content)

	// With tool result.
	resp = sendChatRequest(t, url, ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "call ping"},
			{Role: "assistant", ToolCalls: []ToolCall{{
				ID: "call_123", Type: "function",
				Function: FunctionCall{Name: "ping", Arguments: "{}"},
			}}},
			{Role: "tool", Name: "ping", Content: "pong", ToolCallID: "call_123"},
		},
	})
	require.Equal(t, "Pong received!", resp.Choices[0].Message.Content)
}

func TestServerReset(t *testing.T) {
	t.Parallel()

	server := NewServer()
	server.OnAny(TextResponse("before reset"))
	url := server.Start(t)

	// Make a request.
	sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	require.Len(t, server.Requests(), 1)

	// Reset.
	server.Reset()
	require.Len(t, server.Requests(), 0)

	// After reset, default handler is used.
	resp := sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	require.Contains(t, resp.Choices[0].Message.Content, "don't know")
}

func TestServerStreaming(t *testing.T) {
	t.Parallel()

	server := NewServer()
	server.OnAny(TextResponse("Hello streaming world!"))
	url := server.Start(t)

	// Send streaming request.
	reqBody := ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Stream:   true,
	}
	body, err := json.Marshal(reqBody)
	require.NoError(t, err)

	resp, err := http.Post(url+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Parse SSE stream.
	chunks, err := ParseSSEStream(resp.Body)
	require.NoError(t, err)
	require.NotEmpty(t, chunks)

	// Reconstruct content from chunks.
	var content string
	for _, chunk := range chunks {
		if len(chunk.Choices) > 0 {
			content += chunk.Choices[0].Delta.Content
		}
	}
	require.Equal(t, "Hello streaming world!", content)
}

func TestMatcherCombinators(t *testing.T) {
	t.Parallel()

	server := NewServer()

	// Use And combinator.
	server.On(
		And(MessageContains("hello"), HasSystemPrompt()),
		TextResponse("Matched both conditions!"),
	)

	// Use Or combinator.
	server.On(
		Or(MessageContains("foo"), MessageContains("bar")),
		TextResponse("Matched foo or bar!"),
	)

	server.Default(TextResponse("No match"))
	url := server.Start(t)

	// Test And - both conditions met.
	resp := sendChatRequest(t, url, ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "say hello"},
		},
	})
	require.Equal(t, "Matched both conditions!", resp.Choices[0].Message.Content)

	// Test And - only one condition met.
	server.Reset()
	server.On(
		And(MessageContains("hello"), HasSystemPrompt()),
		TextResponse("Matched both conditions!"),
	)
	server.Default(TextResponse("No match"))

	resp = sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "say hello"}},
	})
	require.Equal(t, "No match", resp.Choices[0].Message.Content)

	// Test Or.
	server.Reset()
	server.On(
		Or(MessageContains("foo"), MessageContains("bar")),
		TextResponse("Matched foo or bar!"),
	)
	server.Default(TextResponse("No match"))

	resp = sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "test bar here"}},
	})
	require.Equal(t, "Matched foo or bar!", resp.Choices[0].Message.Content)
}

func TestConversationBuilder(t *testing.T) {
	t.Parallel()

	server := NewServer()
	NewConversation(server).
		ThenText("Hello!").
		ThenTool("search", map[string]string{"query": "test"}).
		ThenText("Here are the results.").
		Apply()

	url := server.Start(t)

	// First response.
	resp := sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	require.Equal(t, "Hello!", resp.Choices[0].Message.Content)

	// Second response - tool call.
	resp = sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "search for something"}},
	})
	require.Len(t, resp.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "search", resp.Choices[0].Message.ToolCalls[0].Function.Name)

	// Third response.
	resp = sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "continue"}},
	})
	require.Equal(t, "Here are the results.", resp.Choices[0].Message.Content)
}

func TestMultiToolCallResponse(t *testing.T) {
	t.Parallel()

	server := NewServer()
	server.OnAny(MultiToolCallResponse(
		ToolCallSpec{Name: "read_file", Arguments: map[string]string{"path": "/a.txt"}},
		ToolCallSpec{Name: "read_file", Arguments: map[string]string{"path": "/b.txt"}},
	))
	url := server.Start(t)

	resp := sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "read both files"}},
	})

	require.Len(t, resp.Choices[0].Message.ToolCalls, 2)
	require.Equal(t, "read_file", resp.Choices[0].Message.ToolCalls[0].Function.Name)
	require.Equal(t, "read_file", resp.Choices[0].Message.ToolCalls[1].Function.Name)
}

func TestEchoResponse(t *testing.T) {
	t.Parallel()

	server := NewServer()
	server.OnAny(EchoResponse("You said: "))
	url := server.Start(t)

	resp := sendChatRequest(t, url, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hello world"}},
	})

	require.Equal(t, "You said: hello world", resp.Choices[0].Message.Content)
}

// sendChatRequest is a helper to send a chat request to the mock server.
func sendChatRequest(t *testing.T, baseURL string, req ChatRequest) *ChatResponse {
	t.Helper()

	body, err := json.Marshal(req)
	require.NoError(t, err)

	resp, err := http.Post(baseURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	var chatResp ChatResponse
	err = json.NewDecoder(resp.Body).Decode(&chatResp)
	require.NoError(t, err)

	return &chatResp
}

func TestTestConfig(t *testing.T) {
	t.Parallel()

	// Create a test server to get a URL.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()

	config := TestConfig(ts.URL)

	// Verify the config is valid JSON and contains the URL.
	var parsed map[string]any
	err := json.Unmarshal([]byte(config), &parsed)
	require.NoError(t, err)

	providers := parsed["providers"].(map[string]any)
	mock := providers["mock"].(map[string]any)
	require.Equal(t, ts.URL, mock["base_url"])
}
