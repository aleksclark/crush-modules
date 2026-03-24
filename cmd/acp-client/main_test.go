package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestParseSSEStream(t *testing.T) {
	t.Parallel()

	t.Run("single event", func(t *testing.T) {
		t.Parallel()
		input := "data: {\"type\":\"run.created\",\"run\":{\"run_id\":\"r1\",\"status\":\"created\"}}\n\n"
		ch := parseSSEStream(strings.NewReader(input))

		ev, ok := <-ch
		if !ok {
			t.Fatal("expected event")
		}
		if ev.Type != "run.created" {
			t.Fatalf("expected type run.created, got %s", ev.Type)
		}
		if ev.Run == nil || ev.Run.RunID != "r1" {
			t.Fatal("expected run with id r1")
		}

		_, ok = <-ch
		if ok {
			t.Fatal("expected channel to close")
		}
	})

	t.Run("multiple events", func(t *testing.T) {
		t.Parallel()
		input := "" +
			"data: {\"type\":\"run.created\",\"run\":{\"run_id\":\"r1\",\"status\":\"created\"}}\n\n" +
			"data: {\"type\":\"message.part\",\"part\":{\"content_type\":\"text/plain\",\"content\":\"hello\"}}\n\n" +
			"data: {\"type\":\"run.completed\",\"run\":{\"run_id\":\"r1\",\"status\":\"completed\"}}\n\n"

		ch := parseSSEStream(strings.NewReader(input))

		types := []string{}
		for ev := range ch {
			types = append(types, ev.Type)
		}
		if len(types) != 3 {
			t.Fatalf("expected 3 events, got %d: %v", len(types), types)
		}
		if types[0] != "run.created" || types[1] != "message.part" || types[2] != "run.completed" {
			t.Fatalf("unexpected event types: %v", types)
		}
	})

	t.Run("multiline data", func(t *testing.T) {
		t.Parallel()
		input := "data: {\"type\":\"message.part\",\ndata: \"part\":{\"content_type\":\"text/plain\",\"content\":\"hi\"}}\n\n"
		ch := parseSSEStream(strings.NewReader(input))

		ev := <-ch
		if ev.Type != "message.part" {
			t.Fatalf("expected message.part, got %s", ev.Type)
		}
	})

	t.Run("ignores comments and unknown fields", func(t *testing.T) {
		t.Parallel()
		input := ": this is a comment\nevent: ignore\nid: 123\nretry: 5000\ndata: {\"type\":\"run.created\",\"run\":{\"run_id\":\"r1\",\"status\":\"created\"}}\n\n"
		ch := parseSSEStream(strings.NewReader(input))

		ev := <-ch
		if ev.Type != "run.created" {
			t.Fatalf("expected run.created, got %s", ev.Type)
		}
	})

	t.Run("invalid JSON emits error event", func(t *testing.T) {
		t.Parallel()
		input := "data: not-json\n\n"
		ch := parseSSEStream(strings.NewReader(input))

		ev := <-ch
		if ev.Type != "error" {
			t.Fatalf("expected error event, got %s", ev.Type)
		}
		if ev.Error == nil {
			t.Fatal("expected error to be set")
		}
	})

	t.Run("empty stream", func(t *testing.T) {
		t.Parallel()
		ch := parseSSEStream(strings.NewReader(""))

		_, ok := <-ch
		if ok {
			t.Fatal("expected channel to close immediately")
		}
	})
}

func TestNewUserMessage(t *testing.T) {
	t.Parallel()
	msg := NewUserMessage("hello world")
	if msg.Role != "user" {
		t.Fatalf("expected role user, got %s", msg.Role)
	}
	if len(msg.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(msg.Parts))
	}
	if msg.Parts[0].ContentType != "text/plain" {
		t.Fatalf("expected text/plain, got %s", msg.Parts[0].ContentType)
	}
	if msg.Parts[0].Content != "hello world" {
		t.Fatalf("expected hello world, got %s", msg.Parts[0].Content)
	}
}

func TestClientListAgents(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/agents" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(AgentsListResponse{
				Agents: []AgentManifest{
					{Name: "crush", Description: "AI assistant"},
				},
			})
		}))
		defer server.Close()

		client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
		agents, err := client.ListAgents(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(agents) != 1 {
			t.Fatalf("expected 1 agent, got %d", len(agents))
		}
		if agents[0].Name != "crush" {
			t.Fatalf("expected agent name crush, got %s", agents[0].Name)
		}
	})

	t.Run("server error", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ACPError{Code: 500, Message: "internal error"})
		}))
		defer server.Close()

		client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
		_, err := client.ListAgents(context.Background())
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Fatalf("expected 500 in error, got: %v", err)
		}
	})

	t.Run("custom headers", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Errorf("expected auth header, got: %s", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(AgentsListResponse{})
		}))
		defer server.Close()

		client := &Client{
			BaseURL:    server.URL,
			HTTPClient: server.Client(),
			Headers:    map[string]string{"Authorization": "Bearer test-token"},
		}
		_, err := client.ListAgents(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestClientCreateRunStream(t *testing.T) {
	t.Parallel()

	t.Run("streams events", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/runs" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.Header.Get("Accept") != "text/event-stream" {
				t.Errorf("expected Accept: text/event-stream, got: %s", r.Header.Get("Accept"))
			}

			var body RunCreateRequest
			json.NewDecoder(r.Body).Decode(&body)
			if body.AgentName != "crush" {
				t.Errorf("expected agent_name crush, got %s", body.AgentName)
			}
			if body.Mode != "stream" {
				t.Errorf("expected mode stream, got %s", body.Mode)
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)

			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("expected flusher")
			}

			events := []Event{
				{Type: "run.created", Run: &Run{RunID: "r1", Status: "created"}},
				{Type: "message.part", Part: &MessagePart{ContentType: "text/plain", Content: "Hello "}},
				{Type: "message.part", Part: &MessagePart{ContentType: "text/plain", Content: "world!"}},
				{Type: "run.completed", Run: &Run{RunID: "r1", Status: "completed"}},
			}

			for _, ev := range events {
				data, _ := json.Marshal(ev)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}))
		defer server.Close()

		client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
		req := RunCreateRequest{
			AgentName: "crush",
			Input:     []Message{NewUserMessage("hi")},
			Mode:      "stream",
		}

		events, errCh, err := client.CreateRunStream(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var collected []Event
		for ev := range events {
			collected = append(collected, ev)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}

		if len(collected) != 4 {
			t.Fatalf("expected 4 events, got %d", len(collected))
		}

		// Check message parts were streamed.
		var text string
		for _, ev := range collected {
			if ev.Type == "message.part" && ev.Part != nil {
				text += ev.Part.Content
			}
		}
		if text != "Hello world!" {
			t.Fatalf("expected 'Hello world!', got %q", text)
		}
	})

	t.Run("server error response", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ACPError{Code: 400, Message: "bad request"})
		}))
		defer server.Close()

		client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
		_, _, err := client.CreateRunStream(context.Background(), RunCreateRequest{
			AgentName: "crush",
			Input:     []Message{NewUserMessage("hi")},
			Mode:      "stream",
		})
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "400") {
			t.Fatalf("expected 400 in error, got: %v", err)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		t.Parallel()

		started := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)

			data, _ := json.Marshal(Event{Type: "run.created", Run: &Run{RunID: "r1", Status: "created"}})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

			close(started)
			// Block until context is cancelled.
			<-r.Context().Done()
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}

		events, errCh, err := client.CreateRunStream(ctx, RunCreateRequest{
			AgentName: "crush",
			Input:     []Message{NewUserMessage("hi")},
			Mode:      "stream",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Wait for at least one event.
		<-started
		ev := <-events
		if ev.Type != "run.created" {
			t.Fatalf("expected run.created, got %s", ev.Type)
		}

		cancel()

		// Drain remaining.
		for range events {
		}
		if err := <-errCh; err != nil && err != context.Canceled {
			t.Fatalf("expected context.Canceled or nil, got: %v", err)
		}
	})

	t.Run("session ID is sent", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body RunCreateRequest
			json.NewDecoder(r.Body).Decode(&body)
			if body.SessionID != "my-session" {
				t.Errorf("expected session_id my-session, got %s", body.SessionID)
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			data, _ := json.Marshal(Event{Type: "run.completed", Run: &Run{RunID: "r1", Status: "completed", SessionID: "my-session"}})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}))
		defer server.Close()

		client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
		events, errCh, err := client.CreateRunStream(context.Background(), RunCreateRequest{
			AgentName: "crush",
			Input:     []Message{NewUserMessage("hi")},
			SessionID: "my-session",
			Mode:      "stream",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for range events {
		}
		if err := <-errCh; err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestStreamRun(t *testing.T) {
	t.Parallel()

	t.Run("collects text output", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)

			events := []Event{
				{Type: "run.created", Run: &Run{RunID: "r1", Status: "created"}},
				{Type: "message.part", Part: &MessagePart{ContentType: "text/plain", Content: "Hello "}},
				{Type: "message.part", Part: &MessagePart{ContentType: "text/plain", Content: "world!"}},
				{Type: "run.completed", Run: &Run{RunID: "r1", Status: "completed"}},
			}
			for _, ev := range events {
				data, _ := json.Marshal(ev)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}))
		defer server.Close()

		client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
		var buf strings.Builder
		_, err := streamRun(context.Background(), client, "crush", "", "hi", &buf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if buf.String() != "Hello world!" {
			t.Fatalf("expected 'Hello world!', got %q", buf.String())
		}
	})

	t.Run("reports run failure", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)

			ev := Event{Type: "run.failed", Run: &Run{RunID: "r1", Status: "failed", Error: &ACPError{Message: "something broke"}}}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}))
		defer server.Close()

		client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
		var buf strings.Builder
		_, err := streamRun(context.Background(), client, "crush", "", "hi", &buf)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "something broke") {
			t.Fatalf("expected 'something broke' in error, got: %v", err)
		}
	})

	t.Run("captures session ID from run events", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)

			events := []Event{
				{Type: "run.created", Run: &Run{RunID: "r1", SessionID: "ses-from-server", Status: "created"}},
				{Type: "message.part", Part: &MessagePart{ContentType: "text/plain", Content: "ok"}},
				{Type: "run.completed", Run: &Run{RunID: "r1", SessionID: "ses-from-server", Status: "completed"}},
			}
			for _, ev := range events {
				data, _ := json.Marshal(ev)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}))
		defer server.Close()

		client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
		var buf strings.Builder
		result, err := streamRun(context.Background(), client, "crush", "", "hi", &buf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.SessionID != "ses-from-server" {
			t.Fatalf("expected session ID 'ses-from-server', got %q", result.SessionID)
		}
	})
}

func TestIsTerminal(t *testing.T) {
	t.Parallel()

	// A temp file is not a char device.
	f, err := os.CreateTemp(t.TempDir(), "test")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer f.Close()

	if isTerminal(f) {
		t.Fatal("temp file should not be a terminal")
	}
}

func TestRunCreateRequestJSON(t *testing.T) {
	t.Parallel()

	req := RunCreateRequest{
		AgentName: "crush",
		Input:     []Message{NewUserMessage("test prompt")},
		SessionID: "ses-123",
		Mode:      "stream",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded RunCreateRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.AgentName != "crush" {
		t.Fatalf("expected crush, got %s", decoded.AgentName)
	}
	if decoded.SessionID != "ses-123" {
		t.Fatalf("expected ses-123, got %s", decoded.SessionID)
	}
	if decoded.Mode != "stream" {
		t.Fatalf("expected stream, got %s", decoded.Mode)
	}
	if len(decoded.Input) != 1 {
		t.Fatalf("expected 1 input message, got %d", len(decoded.Input))
	}
	if decoded.Input[0].Parts[0].Content != "test prompt" {
		t.Fatalf("expected 'test prompt', got %q", decoded.Input[0].Parts[0].Content)
	}
}

func TestRunCreateRequestOmitsEmptySessionID(t *testing.T) {
	t.Parallel()

	req := RunCreateRequest{
		AgentName: "crush",
		Input:     []Message{NewUserMessage("hi")},
		Mode:      "stream",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if strings.Contains(string(data), "session_id") {
		t.Fatalf("expected session_id to be omitted, got: %s", string(data))
	}
}
