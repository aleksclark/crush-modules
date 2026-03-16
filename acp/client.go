package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Client is an HTTP client for the ACP protocol.
type Client struct {
	baseURL    string
	httpClient *http.Client
	headers    map[string]string
	logger     *slog.Logger
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) ClientOption {
	return func(cl *Client) { cl.httpClient = c }
}

// WithHeaders sets custom headers sent with every request.
func WithHeaders(h map[string]string) ClientOption {
	return func(cl *Client) { cl.headers = h }
}

// WithLogger sets the logger for the client.
func WithLogger(l *slog.Logger) ClientOption {
	return func(cl *Client) { cl.logger = l }
}

// NewClient creates an ACP client for the given base URL.
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ListAgents returns agents available on the ACP server.
func (c *Client) ListAgents(ctx context.Context, limit, offset int) ([]AgentManifest, error) {
	if limit <= 0 {
		limit = 10
	}
	url := fmt.Sprintf("%s/agents?limit=%d&offset=%d", c.baseURL, limit, offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		return nil, c.readError(resp)
	}

	var result AgentsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Agents, nil
}

// GetAgent returns the manifest of a specific agent.
func (c *Client) GetAgent(ctx context.Context, name string) (*AgentManifest, error) {
	url := fmt.Sprintf("%s/agents/%s", c.baseURL, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.readError(resp)
	}

	var manifest AgentManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &manifest, nil
}

// CreateRunSync creates a run and blocks until completion.
func (c *Client) CreateRunSync(ctx context.Context, agentName string, input []Message, sessionID string) (*Run, error) {
	body := RunCreateRequest{
		AgentName: agentName,
		Input:     input,
		SessionID: sessionID,
		Mode:      RunModeSync,
	}
	return c.createRun(ctx, body)
}

// CreateRunAsync creates a run and returns immediately with a run ID.
func (c *Client) CreateRunAsync(ctx context.Context, agentName string, input []Message, sessionID string) (*Run, error) {
	body := RunCreateRequest{
		AgentName: agentName,
		Input:     input,
		SessionID: sessionID,
		Mode:      RunModeAsync,
	}
	return c.createRun(ctx, body)
}

// CreateRunStream creates a run and returns a channel of SSE events.
func (c *Client) CreateRunStream(ctx context.Context, agentName string, input []Message, sessionID string) (<-chan Event, error) {
	body := RunCreateRequest{
		AgentName: agentName,
		Input:     input,
		SessionID: sessionID,
		Mode:      RunModeStream,
	}
	return c.createRunStream(ctx, body)
}

// GetRun retrieves the current state of a run.
func (c *Client) GetRun(ctx context.Context, runID string) (*Run, error) {
	url := fmt.Sprintf("%s/runs/%s", c.baseURL, runID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.readError(resp)
	}

	var run Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &run, nil
}

// ResumeRun resumes an awaiting run with new input.
func (c *Client) ResumeRun(ctx context.Context, runID string, resume *AwaitResume) (*Run, error) {
	body := RunResumeRequest{
		RunID:       runID,
		AwaitResume: resume,
		Mode:        RunModeSync,
	}
	return c.resumeRun(ctx, runID, body)
}

// ResumeRunStream resumes an awaiting run and returns a channel of SSE events.
func (c *Client) ResumeRunStream(ctx context.Context, runID string, resume *AwaitResume) (<-chan Event, error) {
	body := RunResumeRequest{
		RunID:       runID,
		AwaitResume: resume,
		Mode:        RunModeStream,
	}
	return c.resumeRunStream(ctx, runID, body)
}

// CancelRun requests cancellation of a run.
func (c *Client) CancelRun(ctx context.Context, runID string) (*Run, error) {
	url := fmt.Sprintf("%s/runs/%s/cancel", c.baseURL, runID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cancel run: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, c.readError(resp)
	}

	var run Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &run, nil
}

// PollRun polls a run until it reaches a terminal state or the context is cancelled.
func (c *Client) PollRun(ctx context.Context, runID string, interval time.Duration) (*Run, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		run, err := c.GetRun(ctx, runID)
		if err != nil {
			return nil, err
		}
		if run.Status.IsTerminal() || run.Status == RunStatusAwaiting {
			return run, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Client) createRun(ctx context.Context, body RunCreateRequest) (*Run, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/runs", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, c.readError(resp)
	}

	var run Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &run, nil
}

func (c *Client) createRunStream(ctx context.Context, body RunCreateRequest) (<-chan Event, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/runs", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create run stream: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, c.readError(resp)
	}

	// Stream body is kept open; the SSE parser goroutine owns closing it.
	return ParseSSEStream(resp.Body), nil
}

func (c *Client) resumeRun(ctx context.Context, runID string, body RunResumeRequest) (*Run, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/runs/%s", c.baseURL, runID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("resume run: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, c.readError(resp)
	}

	var run Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &run, nil
}

func (c *Client) resumeRunStream(ctx context.Context, runID string, body RunResumeRequest) (<-chan Event, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/runs/%s", c.baseURL, runID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("resume run stream: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, c.readError(resp)
	}

	return ParseSSEStream(resp.Body), nil
}

func (c *Client) setHeaders(req *http.Request) {
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
}

func (c *Client) readError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var acpErr ACPError
	if json.Unmarshal(body, &acpErr) == nil && acpErr.Message != "" {
		return fmt.Errorf("ACP error (HTTP %d): %s", resp.StatusCode, acpErr.Message)
	}
	return fmt.Errorf("ACP error (HTTP %d): %s", resp.StatusCode, string(body))
}
