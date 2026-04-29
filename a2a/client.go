package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	a2acore "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
)

// Client wraps the official A2A SDK client with connection caching.
type Client struct {
	baseURL string
	headers map[string]string
	logger  *slog.Logger

	mu        sync.Mutex
	client    *a2aclient.Client
	card      *a2acore.AgentCard
	contextID string
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithHeaders sets custom headers for requests.
func WithHeaders(h map[string]string) ClientOption {
	return func(c *Client) { c.headers = h }
}

// WithLogger sets a custom logger.
func WithLogger(l *slog.Logger) ClientOption {
	return func(c *Client) { c.logger = l }
}

// NewClient creates a new A2A client for the given base URL.
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL: baseURL,
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) getClient(ctx context.Context) (*a2aclient.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil {
		return c.client, nil
	}

	endpoint := a2acore.NewAgentInterface(c.baseURL, a2acore.TransportProtocolJSONRPC)
	client, err := a2aclient.NewFromEndpoints(ctx, []*a2acore.AgentInterface{endpoint})
	if err != nil {
		return nil, fmt.Errorf("create A2A client for %s: %w", c.baseURL, err)
	}
	c.client = client
	return client, nil
}

// SendMessage sends a message to the remote A2A agent.
func (c *Client) SendMessage(ctx context.Context, msg *a2acore.Message) (a2acore.SendMessageResult, error) {
	client, err := c.getClient(ctx)
	if err != nil {
		return nil, err
	}
	req := &a2acore.SendMessageRequest{Message: msg}
	return client.SendMessage(ctx, req)
}

// SendStreamingMessage sends a message and returns a streaming iterator.
func (c *Client) SendStreamingMessage(ctx context.Context, msg *a2acore.Message) (iter.Seq2[a2acore.Event, error], error) {
	client, err := c.getClient(ctx)
	if err != nil {
		return nil, err
	}
	req := &a2acore.SendMessageRequest{Message: msg}
	return client.SendStreamingMessage(ctx, req), nil
}

// SendMessageWithContext sends a text message with an existing contextID for multi-turn conversations.
func (c *Client) SendMessageWithContext(ctx context.Context, text, contextID string) (a2acore.SendMessageResult, error) {
	msg := a2acore.NewMessage(a2acore.MessageRoleUser, a2acore.NewTextPart(text))
	msg.ContextID = contextID
	return c.SendMessage(ctx, msg)
}

// GetTask retrieves a task by ID.
func (c *Client) GetTask(ctx context.Context, taskID a2acore.TaskID) (*a2acore.Task, error) {
	client, err := c.getClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.GetTask(ctx, &a2acore.GetTaskRequest{ID: taskID})
}

// CancelTask cancels a task by ID.
func (c *Client) CancelTask(ctx context.Context, taskID a2acore.TaskID) (*a2acore.Task, error) {
	client, err := c.getClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.CancelTask(ctx, &a2acore.CancelTaskRequest{ID: taskID})
}

// FetchAgentCard fetches the agent card from /.well-known/agent-card.json.
func (c *Client) FetchAgentCard(ctx context.Context) (*a2acore.AgentCard, error) {
	c.mu.Lock()
	if c.card != nil {
		card := c.card
		c.mu.Unlock()
		return card, nil
	}
	c.mu.Unlock()

	cardURL := strings.TrimRight(c.baseURL, "/") + "/.well-known/agent-card.json"
	httpClient := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cardURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create agent card request: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch agent card from %s: %w", cardURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("agent card HTTP %d: %s", resp.StatusCode, string(body))
	}

	var card a2acore.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return nil, fmt.Errorf("decode agent card: %w", err)
	}

	c.mu.Lock()
	c.card = &card
	c.mu.Unlock()

	return &card, nil
}

// LastContextID returns the last contextId received from this server.
func (c *Client) LastContextID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.contextID
}

// SetContextID stores a contextId for auto-propagation in subsequent messages.
func (c *Client) SetContextID(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.contextID = id
}

// Close destroys the underlying SDK client.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil {
		err := c.client.Destroy()
		c.client = nil
		return err
	}
	return nil
}
