package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a high-level SDK client for the Crush ACP protocol.
//
// It provides session-oriented methods for multi-turn conversations with
// Crush agents, including session persistence via export/import.
type Client struct {
	baseURL    string
	agentName  string
	httpClient *http.Client
	headers    map[string]string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets a custom HTTP client for all requests.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.httpClient = c }
}

// WithHeaders sets custom headers sent with every request.
func WithHeaders(h map[string]string) Option {
	return func(cl *Client) { cl.headers = h }
}

// WithAgentName sets the agent name used for all runs. If not set, the client
// auto-detects by querying GET /agents and using the first available agent.
func WithAgentName(name string) Option {
	return func(cl *Client) { cl.agentName = name }
}

// NewClient creates an ACP SDK client for the given server URL.
//
//	client := sdk.NewClient("http://localhost:8199")
//	client := sdk.NewClient("http://localhost:8199", sdk.WithAgentName("crush"))
func NewClient(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 0, // no timeout — runs can be long
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// SessionResult is the outcome of a synchronous prompt execution.
type SessionResult struct {
	// Run contains the full run state including status and output.
	Run *Run

	// Snapshot is the session snapshot captured from the session.snapshot
	// streaming event, if available. Only populated by streaming methods.
	Snapshot *SessionSnapshot
}

// Text returns the concatenated text/plain content from the run output.
func (r *SessionResult) Text() string {
	if r.Run == nil {
		return ""
	}
	return TextContent(r.Run.Output)
}

// Stream is a handle to a streaming run. Read events from the Events channel
// until it closes, then check Err() for any error.
type Stream struct {
	// Events receives streaming events until the run reaches a terminal state.
	Events <-chan Event

	err      error
	resultCh chan *SessionResult
}

// Err returns any error that occurred during streaming. Only valid after the
// Events channel is closed.
func (s *Stream) Err() error {
	return s.err
}

// Result blocks until the stream completes and returns the final result.
// This consumes all events from the Events channel.
func (s *Stream) Result() (*SessionResult, error) {
	for range s.Events {
		// drain
	}
	return <-s.resultCh, s.err
}

// Ping checks if the ACP server is reachable.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/ping", nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	if resp.StatusCode != http.StatusOK || string(body) != "pong" {
		return fmt.Errorf("ping: unexpected response (HTTP %d): %s", resp.StatusCode, body)
	}
	return nil
}

// ListAgents returns agents available on the ACP server.
func (c *Client) ListAgents(ctx context.Context) ([]AgentManifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/agents", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result agentsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Agents, nil
}

// NewSession starts a new session by sending a prompt to the agent.
// The returned result contains the run (with output and session ID) and
// the text response.
func (c *Client) NewSession(ctx context.Context, prompt string) (*SessionResult, error) {
	return c.runSync(ctx, "", prompt)
}

// Resume continues an existing session with a new prompt. The session ID
// must be from a previous NewSession or Restore call.
func (c *Client) Resume(ctx context.Context, sessionID, prompt string) (*SessionResult, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required for Resume")
	}
	return c.runSync(ctx, sessionID, prompt)
}

// NewSessionStream starts a new session with streaming output.
// Read events from stream.Events until the channel closes.
func (c *Client) NewSessionStream(ctx context.Context, prompt string) (*Stream, error) {
	return c.runStream(ctx, "", prompt)
}

// ResumeStream continues an existing session with streaming output.
func (c *Client) ResumeStream(ctx context.Context, sessionID, prompt string) (*Stream, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required for ResumeStream")
	}
	return c.runStream(ctx, sessionID, prompt)
}

// Dump exports the full session snapshot for the given session ID.
// The snapshot can be persisted and later restored on the same or different
// agent instance.
func (c *Client) Dump(ctx context.Context, sessionID string) (*SessionSnapshot, error) {
	url := fmt.Sprintf("%s/sessions/%s/export", c.baseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("export session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var snapshot SessionSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	return &snapshot, nil
}

// Restore imports a session snapshot into the agent. If a session with the
// same ID already exists on the server, it is replaced.
//
// After restoring, use Resume with the snapshot's session ID to continue.
func (c *Client) Restore(ctx context.Context, snapshot *SessionSnapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/sessions/import", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("import session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}

	var result importResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode import response: %w", err)
	}
	if result.Status != "imported" {
		return fmt.Errorf("unexpected import status: %s", result.Status)
	}
	return nil
}

// --- Internal methods ---

func (c *Client) resolveAgent(ctx context.Context) (string, error) {
	if c.agentName != "" {
		return c.agentName, nil
	}

	agents, err := c.ListAgents(ctx)
	if err != nil {
		return "", fmt.Errorf("auto-detect agent: %w", err)
	}
	if len(agents) == 0 {
		return "", fmt.Errorf("no agents available on %s", c.baseURL)
	}
	c.agentName = agents[0].Name
	return c.agentName, nil
}

func (c *Client) runSync(ctx context.Context, sessionID, prompt string) (*SessionResult, error) {
	agent, err := c.resolveAgent(ctx)
	if err != nil {
		return nil, err
	}

	body := runCreateRequest{
		AgentName: agent,
		Input:     []Message{NewUserMessage(prompt)},
		SessionID: sessionID,
		Mode:      RunModeSync,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Sync runs can be long; use a client without timeout.
	httpClient := c.httpClient
	if httpClient.Timeout > 0 {
		clone := *httpClient
		clone.Timeout = 0
		httpClient = &clone
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/runs", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, readError(resp)
	}

	var run Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &SessionResult{Run: &run}, nil
}

func (c *Client) runStream(ctx context.Context, sessionID, prompt string) (*Stream, error) {
	agent, err := c.resolveAgent(ctx)
	if err != nil {
		return nil, err
	}

	body := runCreateRequest{
		AgentName: agent,
		Input:     []Message{NewUserMessage(prompt)},
		SessionID: sessionID,
		Mode:      RunModeStream,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/runs", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", contentTypeNDJSON)
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create run stream: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, readError(resp)
	}

	rawEvents := parseStream(resp.Body)

	events := make(chan Event, 16)
	resultCh := make(chan *SessionResult, 1)
	stream := &Stream{
		Events:   events,
		resultCh: resultCh,
	}

	go func() {
		defer resp.Body.Close()
		defer close(events)

		result := &SessionResult{}
		for ev := range rawEvents {
			select {
			case events <- ev:
			case <-ctx.Done():
				stream.err = ctx.Err()
				resultCh <- result
				return
			}

			if ev.Run != nil {
				result.Run = ev.Run
			}
			if ev.Type == EventSessionSnapshot && ev.Generic != nil {
				if snap, ok := decodeSnapshot(ev.Generic); ok {
					result.Snapshot = snap
				}
			}
		}
		resultCh <- result
	}()

	return stream, nil
}

func decodeSnapshot(v any) (*SessionSnapshot, bool) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var snap SessionSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, false
	}
	if snap.Version == 0 {
		return nil, false
	}
	return &snap, true
}

// WaitReady polls the server until it responds to /ping or the context
// is cancelled. Useful for waiting after spawning an agent container.
func (c *Client) WaitReady(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := c.Ping(ctx); err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("server not ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (c *Client) setHeaders(req *http.Request) {
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
}

func readError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var acpErr ACPError
	if json.Unmarshal(body, &acpErr) == nil && acpErr.Message != "" {
		return fmt.Errorf("ACP error (HTTP %d): %s", resp.StatusCode, acpErr.Message)
	}
	return fmt.Errorf("ACP error (HTTP %d): %s", resp.StatusCode, string(body))
}
