package a2a

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"sync"

	a2acore "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
)

// Client wraps the official A2A SDK client with connection caching.
type Client struct {
	baseURL string
	headers map[string]string
	logger  *slog.Logger

	mu     sync.Mutex
	client *a2aclient.Client
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
