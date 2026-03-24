// acp-client is a standalone CLI chat client for Crush's ACP server.
//
// It connects to a running Crush instance over HTTP and provides an
// interactive terminal for multi-turn conversations with streaming output.
//
// Usage:
//
//	acp-client [flags]
//	acp-client -url http://localhost:8199
//	acp-client -url http://localhost:8199 -session my-session
//	echo "explain auth.go" | acp-client -url http://localhost:8199
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
)

func main() {
	var (
		serverURL string
		agentName string
		sessionID string
		oneShot   bool
	)

	flag.StringVar(&serverURL, "url", "http://localhost:8199", "ACP server URL")
	flag.StringVar(&agentName, "agent", "", "agent name (auto-detected if empty)")
	flag.StringVar(&sessionID, "session", "", "session ID for multi-turn (generated if empty)")
	flag.BoolVar(&oneShot, "once", false, "send one prompt from stdin and exit")
	flag.Parse()

	serverURL = strings.TrimRight(serverURL, "/")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client := &Client{
		BaseURL:    serverURL,
		HTTPClient: &http.Client{},
	}

	if agentName == "" {
		agents, err := client.ListAgents(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot reach server at %s: %v\n", serverURL, err)
			os.Exit(1)
		}
		if len(agents) == 0 {
			fmt.Fprintf(os.Stderr, "error: no agents available on %s\n", serverURL)
			os.Exit(1)
		}
		agentName = agents[0].Name
	}

	if oneShot || !isTerminal(os.Stdin) {
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading stdin: %v\n", err)
			os.Exit(1)
		}
		prompt := strings.TrimSpace(string(input))
		if prompt == "" {
			// Check for positional args.
			if flag.NArg() > 0 {
				prompt = strings.Join(flag.Args(), " ")
			}
		}
		if prompt == "" {
			fmt.Fprintf(os.Stderr, "error: no input provided\n")
			os.Exit(1)
		}
		_, err = streamRun(ctx, client, agentName, sessionID, prompt, os.Stdout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout)
		return
	}

	fmt.Fprintf(os.Stderr, "Connected to %s (agent: %s)\n", serverURL, agentName)
	if sessionID != "" {
		fmt.Fprintf(os.Stderr, "Session: %s\n", sessionID)
	}
	fmt.Fprintf(os.Stderr, "Type your message and press Enter. Ctrl+C or 'exit' to quit.\n\n")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Fprint(os.Stderr, "> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			break
		}

		result, err := streamRun(ctx, client, agentName, sessionID, line, os.Stdout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if sessionID == "" && result.SessionID != "" {
			sessionID = result.SessionID
			fmt.Fprintf(os.Stderr, "Session: %s\n", sessionID)
		}
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout)
	}
}

// runResult contains the outcome of a streaming run.
type runResult struct {
	SessionID string
}

func streamRun(ctx context.Context, client *Client, agentName, sessionID, prompt string, w io.Writer) (runResult, error) {
	req := RunCreateRequest{
		AgentName: agentName,
		Input: []Message{
			NewUserMessage(prompt),
		},
		SessionID: sessionID,
		Mode:      "stream",
	}

	events, errCh, err := client.CreateRunStream(ctx, req)
	if err != nil {
		return runResult{}, err
	}

	var result runResult
	for ev := range events {
		switch ev.Type {
		case "run.created", "run.in-progress", "run.completed":
			if ev.Run != nil && ev.Run.SessionID != "" {
				result.SessionID = ev.Run.SessionID
			}
		case "message.part":
			if ev.Part != nil {
				fmt.Fprint(w, ev.Part.Content)
			}
		case "run.failed":
			if ev.Run != nil && ev.Run.Error != nil {
				return result, fmt.Errorf("run failed: %s", ev.Run.Error.Message)
			}
			return result, fmt.Errorf("run failed")
		case "error":
			if ev.Error != nil {
				return result, fmt.Errorf("server error: %s", ev.Error.Message)
			}
		}
	}

	if err := <-errCh; err != nil {
		return result, err
	}
	return result, nil
}

// isTerminal returns true if the given file is a terminal.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// --- ACP Types ---
// Standalone type definitions matching the ACP protocol.
// These are intentionally duplicated from acp/ to keep this client independent.

// RunStatus represents the state of an ACP run.
type RunStatus string

// Message is the core communication unit in ACP.
type Message struct {
	Role  string        `json:"role"`
	Parts []MessagePart `json:"parts"`
}

// MessagePart is a single typed content unit within a message.
type MessagePart struct {
	ContentType string `json:"content_type"`
	Content     string `json:"content,omitempty"`
}

// NewUserMessage creates a text message with the "user" role.
func NewUserMessage(text string) Message {
	return Message{
		Role: "user",
		Parts: []MessagePart{
			{ContentType: "text/plain", Content: text},
		},
	}
}

// Run represents an ACP agent run.
type Run struct {
	AgentName string    `json:"agent_name"`
	RunID     string    `json:"run_id"`
	SessionID string    `json:"session_id,omitempty"`
	Status    RunStatus `json:"status"`
	Output    []Message `json:"output"`
	Error     *ACPError `json:"error,omitempty"`
}

// ACPError represents an error returned by the ACP server.
type ACPError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message"`
}

// RunCreateRequest is the body for POST /runs.
type RunCreateRequest struct {
	AgentName string    `json:"agent_name"`
	Input     []Message `json:"input"`
	SessionID string    `json:"session_id,omitempty"`
	Mode      string    `json:"mode,omitempty"`
}

// AgentManifest describes a remote ACP agent.
type AgentManifest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// AgentsListResponse is the response from GET /agents.
type AgentsListResponse struct {
	Agents []AgentManifest `json:"agents"`
}

// Event is a streaming event from the ACP server.
type Event struct {
	Type    string       `json:"type"`
	Run     *Run         `json:"run,omitempty"`
	Message *Message     `json:"message,omitempty"`
	Part    *MessagePart `json:"part,omitempty"`
	Error   *ACPError    `json:"error,omitempty"`
}

// --- ACP Client ---

// Client is a minimal ACP HTTP client.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	Headers    map[string]string
}

// ListAgents returns the available agents from the server.
func (c *Client) ListAgents(ctx context.Context) ([]AgentManifest, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/agents", nil)
	if err != nil {
		return nil, err
	}
	c.applyHeaders(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result AgentsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode agents response: %w", err)
	}
	return result.Agents, nil
}

// CreateRunStream creates a streaming run and returns a channel of events.
// The error channel receives at most one error when the stream ends.
func (c *Client) CreateRunStream(ctx context.Context, body RunCreateRequest) (<-chan Event, <-chan error, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/runs", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")
	c.applyHeaders(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, nil, readError(resp)
	}

	events := make(chan Event, 16)
	errCh := make(chan error, 1)

	go func() {
		defer resp.Body.Close()
		defer close(events)
		defer close(errCh)

		sseEvents := parseStream(resp.Body)
		for ev := range sseEvents {
			select {
			case events <- ev:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}
	}()

	return events, errCh, nil
}

func (c *Client) applyHeaders(req *http.Request) {
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
}

func readError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var acpErr ACPError
	if json.Unmarshal(body, &acpErr) == nil && acpErr.Message != "" {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, acpErr.Message)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// --- Stream Parser ---

// parseStream reads an NDJSON response body and emits typed events.
// The returned channel is closed when the stream ends.
func parseStream(r io.Reader) <-chan Event {
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var event Event
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				ch <- Event{
					Type:  "error",
					Error: &ACPError{Message: fmt.Sprintf("failed to parse event: %v", err)},
				}
				continue
			}
			ch <- event
		}
	}()
	return ch
}
