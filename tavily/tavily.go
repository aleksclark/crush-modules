// Package tavily provides a Tavily search provider plugin for Crush.
//
// When configured with an API key, this plugin replaces the default DuckDuckGo
// scraper with Tavily's paid search API, eliminating rate-limiting issues.
package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/charmbracelet/crush/plugin"
)

const (
	// PluginName is the name used for configuration and registration.
	PluginName = "tavily"

	// DefaultEndpoint is the Tavily Search API endpoint.
	DefaultEndpoint = "https://api.tavily.com/search"

	// DefaultMaxResults is the default number of results to request.
	DefaultMaxResults = 10

	// DefaultSearchDepth controls latency vs. relevance.
	DefaultSearchDepth = "basic"
)

// Config defines the configuration options for the Tavily plugin.
type Config struct {
	// APIKey is the Tavily API key (required). Starts with "tvly-".
	APIKey string `json:"api_key"`

	// Endpoint overrides the Tavily API URL. Useful for proxies or testing.
	Endpoint string `json:"endpoint,omitempty"`

	// SearchDepth controls result quality. One of: "basic", "advanced".
	SearchDepth string `json:"search_depth,omitempty"`

	// Topic hints the search category. One of: "general", "news", "finance".
	Topic string `json:"topic,omitempty"`

	// IncludeDomains limits results to these domains.
	IncludeDomains []string `json:"include_domains,omitempty"`

	// ExcludeDomains excludes results from these domains.
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
}

func init() {
	plugin.RegisterSearchProviderWithConfig(PluginName, func(ctx context.Context, app *plugin.App) (plugin.SearchProvider, error) {
		var cfg Config
		if err := app.LoadConfig(PluginName, &cfg); err != nil {
			return nil, err
		}
		if cfg.APIKey == "" {
			return nil, nil
		}
		return NewProvider(cfg)
	}, &Config{})
}

// Provider implements plugin.SearchProvider using the Tavily API.
type Provider struct {
	cfg    Config
	client *http.Client
}

// NewProvider creates a new Tavily search provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("tavily: api_key is required")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.SearchDepth == "" {
		cfg.SearchDepth = DefaultSearchDepth
	}

	return &Provider{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// Search executes a Tavily web search.
func (p *Provider) Search(ctx context.Context, query string, maxResults int) ([]plugin.SearchResult, error) {
	if maxResults <= 0 {
		maxResults = DefaultMaxResults
	}

	reqBody := tavilyRequest{
		Query:          query,
		SearchDepth:    p.cfg.SearchDepth,
		MaxResults:     maxResults,
		Topic:          p.cfg.Topic,
		IncludeDomains: p.cfg.IncludeDomains,
		ExcludeDomains: p.cfg.ExcludeDomains,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("tavily: failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("tavily: failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tavily: failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily: API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var tavilyResp tavilyResponse
	if err := json.Unmarshal(respBody, &tavilyResp); err != nil {
		return nil, fmt.Errorf("tavily: failed to parse response: %w", err)
	}

	results := make([]plugin.SearchResult, len(tavilyResp.Results))
	for i, r := range tavilyResp.Results {
		results[i] = plugin.SearchResult{
			Title:    r.Title,
			Link:     r.URL,
			Snippet:  r.Content,
			Position: i + 1,
		}
	}

	return results, nil
}

// tavilyRequest is the JSON request body for the Tavily Search API.
type tavilyRequest struct {
	Query          string   `json:"query"`
	SearchDepth    string   `json:"search_depth,omitempty"`
	MaxResults     int      `json:"max_results,omitempty"`
	Topic          string   `json:"topic,omitempty"`
	IncludeDomains []string `json:"include_domains,omitempty"`
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
}

// tavilyResponse is the JSON response from the Tavily Search API.
type tavilyResponse struct {
	Query        string         `json:"query"`
	Results      []tavilyResult `json:"results"`
	ResponseTime json.Number    `json:"response_time"`
}

// tavilyResult is a single search result from the Tavily API.
type tavilyResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}
