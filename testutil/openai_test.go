package testutil_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/stretchr/testify/require"
)

const astraReply = "Astra regression OK"

type openAIRequest struct {
	path string
	body map[string]any
}

// newOpenAIServer records the real serialized requests, not provider internals.
// It serves both streaming and non-streaming replies for both OpenAI APIs.
func newOpenAIServer(t *testing.T) (string, func() []openAIRequest) {
	t.Helper()
	var mu sync.Mutex
	var requests []openAIRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, openAIRequest{r.URL.Path, body})
		mu.Unlock()

		if _, legacy := body["max_tokens"]; legacy && body["model"] == "gpt-6-astra" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"Unsupported parameter: 'max_tokens'. Use 'max_completion_tokens' instead.","type":"invalid_request_error","param":"max_tokens","code":"unsupported_parameter"}}`)
			return
		}

		stream, _ := body["stream"].(bool)
		w.Header().Set("Content-Type", "application/json")
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		send := func(value any) {
			data, err := json.Marshal(value)
			if err != nil {
				t.Error(err)
				return
			}
			if stream {
				fmt.Fprintf(w, "data: %s\n\n", data)
			} else {
				w.Write(data)
			}
		}

		switch r.URL.Path {
		case "/v1/responses":
			message := map[string]any{
				"id": "msg_test", "type": "message", "role": "assistant", "status": "completed",
				"content": []any{map[string]any{"type": "output_text", "text": astraReply, "annotations": []any{}}},
			}
			response := map[string]any{
				"id": "resp_test", "object": "response", "status": "completed", "model": body["model"],
				"output": []any{message},
				"usage":  map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
			}
			if stream {
				send(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_test", "status": "in_progress"}})
				send(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_test", "type": "message", "role": "assistant", "content": []any{}}})
				send(map[string]any{"type": "response.output_text.delta", "item_id": "msg_test", "output_index": 0, "content_index": 0, "delta": astraReply})
				send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": message})
				send(map[string]any{"type": "response.completed", "response": response})
			} else {
				send(response)
			}
		case "/v1/chat/completions":
			if stream {
				send(map[string]any{
					"id": "chat_test", "object": "chat.completion.chunk", "model": body["model"],
					"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": astraReply}}},
				})
				send(map[string]any{
					"id": "chat_test", "object": "chat.completion.chunk", "model": body["model"],
					"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
				})
				fmt.Fprint(w, "data: [DONE]\n\n")
			} else {
				send(map[string]any{
					"id": "chat_test", "object": "chat.completion", "model": body["model"],
					"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": astraReply}, "finish_reason": "stop"}},
				})
			}
		default:
			http.Error(w, "unexpected endpoint", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL + "/v1", func() []openAIRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]openAIRequest(nil), requests...)
	}
}

func TestOpenAIModelRequests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, model, path, tokenField string
		responses, compat, reasoning  bool
	}{
		{"astra-responses", "gpt-6-astra", "/v1/responses", "max_output_tokens", true, false, true},
		{"astra-chat", "gpt-6-astra", "/v1/chat/completions", "max_completion_tokens", false, false, true},
		{"astra-compat", "gpt-6-astra", "/v1/chat/completions", "max_completion_tokens", false, true, true},
		{"gpt5-chat", "gpt-5", "/v1/chat/completions", "max_completion_tokens", false, false, true},
		{"legacy-chat", "gpt-4o", "/v1/chat/completions", "max_tokens", false, false, false},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				t.Parallel()
				url, requests := newOpenAIServer(t)
				var provider fantasy.Provider
				var err error
				if tc.compat {
					provider, err = openaicompat.New(openaicompat.WithBaseURL(url), openaicompat.WithAPIKey("test-key"))
				} else {
					opts := []openai.Option{openai.WithBaseURL(url), openai.WithAPIKey("test-key")}
					if tc.responses {
						opts = append(opts, openai.WithUseResponsesAPI())
					}
					provider, err = openai.New(opts...)
				}
				require.NoError(t, err)
				model, err := provider.LanguageModel(t.Context(), tc.model)
				require.NoError(t, err)
				call := fantasy.Call{
					Prompt:          fantasy.Prompt{{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "test"}}}},
					MaxOutputTokens: new(int64(4096)), Temperature: new(0.5), TopP: new(0.9),
				}
				if tc.responses {
					call.ProviderOptions = openai.NewResponsesProviderOptions(&openai.ResponsesProviderOptions{
						ReasoningEffort: new(openai.ReasoningEffortXHigh), ReasoningSummary: new("auto"),
					})
				} else if tc.compat {
					call.ProviderOptions = openaicompat.NewProviderOptions(&openaicompat.ProviderOptions{ReasoningEffort: new(openai.ReasoningEffortXHigh)})
				} else if tc.reasoning {
					call.ProviderOptions = openai.NewProviderOptions(&openai.ProviderOptions{ReasoningEffort: new(openai.ReasoningEffortXHigh)})
				}
				if stream {
					parts, err := model.Stream(t.Context(), call)
					require.NoError(t, err)
					var text strings.Builder
					var finished bool
					for part := range parts {
						require.NoError(t, part.Error)
						if part.Type == fantasy.StreamPartTypeTextDelta {
							text.WriteString(part.Delta)
						}
						if part.Type == fantasy.StreamPartTypeFinish {
							finished = true
						}
					}
					require.True(t, finished)
					require.Equal(t, astraReply, text.String())
				} else {
					result, err := model.Generate(t.Context(), call)
					require.NoError(t, err)
					require.Equal(t, astraReply, result.Content.Text())
				}
				got := requests()
				require.Len(t, got, 1)
				require.Equal(t, tc.path, got[0].path)
				body := got[0].body
				require.Equal(t, tc.model, body["model"])
				require.Equal(t, float64(4096), body[tc.tokenField])
				for _, field := range []string{"max_tokens", "max_output_tokens", "max_completion_tokens"} {
					if field != tc.tokenField {
						require.NotContains(t, body, field)
					}
				}
				if tc.reasoning {
					require.NotContains(t, body, "temperature")
					require.NotContains(t, body, "top_p")
					if tc.responses {
						require.Equal(t, map[string]any{"effort": "xhigh", "summary": "auto"}, body["reasoning"])
					} else {
						require.Equal(t, "xhigh", body["reasoning_effort"])
					}
				} else {
					require.Equal(t, 0.5, body["temperature"])
					require.Equal(t, 0.9, body["top_p"])
				}
			})
		}
	}
}
