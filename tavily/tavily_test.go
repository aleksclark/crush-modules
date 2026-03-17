package tavily

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/crush/plugin"
	"github.com/stretchr/testify/require"
)

func TestNewProvider(t *testing.T) {
	t.Parallel()

	t.Run("requires api key", func(t *testing.T) {
		t.Parallel()
		_, err := NewProvider(Config{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "api_key is required")
	})

	t.Run("sets defaults", func(t *testing.T) {
		t.Parallel()
		p, err := NewProvider(Config{APIKey: "tvly-test"})
		require.NoError(t, err)
		require.Equal(t, DefaultEndpoint, p.cfg.Endpoint)
		require.Equal(t, DefaultSearchDepth, p.cfg.SearchDepth)
	})

	t.Run("respects custom endpoint", func(t *testing.T) {
		t.Parallel()
		p, err := NewProvider(Config{APIKey: "tvly-test", Endpoint: "https://custom.api/search"})
		require.NoError(t, err)
		require.Equal(t, "https://custom.api/search", p.cfg.Endpoint)
	})
}

func TestProviderSearch(t *testing.T) {
	t.Parallel()

	t.Run("successful search", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			require.Equal(t, "Bearer tvly-test-key", r.Header.Get("Authorization"))

			var req tavilyRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			require.NoError(t, err)
			require.Equal(t, "golang concurrency", req.Query)
			require.Equal(t, 5, req.MaxResults)
			require.Equal(t, "basic", req.SearchDepth)

			resp := tavilyResponse{
				Query: req.Query,
				Results: []tavilyResult{
					{
						Title:   "Go Concurrency Patterns",
						URL:     "https://go.dev/blog/pipelines",
						Content: "Go provides rich support for concurrency via goroutines and channels.",
						Score:   0.95,
					},
					{
						Title:   "Effective Go - Concurrency",
						URL:     "https://go.dev/doc/effective_go#concurrency",
						Content: "Go's approach to concurrency differs from the traditional use of threads.",
						Score:   0.88,
					},
				},
				ResponseTime: "0.42",
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		p, err := NewProvider(Config{
			APIKey:   "tvly-test-key",
			Endpoint: server.URL,
		})
		require.NoError(t, err)

		results, err := p.Search(context.Background(), "golang concurrency", 5)
		require.NoError(t, err)
		require.Len(t, results, 2)

		require.Equal(t, "Go Concurrency Patterns", results[0].Title)
		require.Equal(t, "https://go.dev/blog/pipelines", results[0].Link)
		require.Equal(t, "Go provides rich support for concurrency via goroutines and channels.", results[0].Snippet)
		require.Equal(t, 1, results[0].Position)

		require.Equal(t, "Effective Go - Concurrency", results[1].Title)
		require.Equal(t, 2, results[1].Position)
	})

	t.Run("passes domain filters", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req tavilyRequest
			json.NewDecoder(r.Body).Decode(&req)
			require.Equal(t, []string{"go.dev"}, req.IncludeDomains)
			require.Equal(t, []string{"reddit.com"}, req.ExcludeDomains)
			require.Equal(t, "news", req.Topic)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(tavilyResponse{Results: []tavilyResult{}})
		}))
		defer server.Close()

		p, err := NewProvider(Config{
			APIKey:         "tvly-test",
			Endpoint:       server.URL,
			Topic:          "news",
			IncludeDomains: []string{"go.dev"},
			ExcludeDomains: []string{"reddit.com"},
		})
		require.NoError(t, err)

		results, err := p.Search(context.Background(), "test", 5)
		require.NoError(t, err)
		require.Empty(t, results)
	})

	t.Run("handles API error", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "Invalid API key"}`))
		}))
		defer server.Close()

		p, err := NewProvider(Config{
			APIKey:   "tvly-bad-key",
			Endpoint: server.URL,
		})
		require.NoError(t, err)

		_, err = p.Search(context.Background(), "test", 5)
		require.Error(t, err)
		require.Contains(t, err.Error(), "status 401")
	})

	t.Run("handles rate limit", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": "Rate limit exceeded"}`))
		}))
		defer server.Close()

		p, err := NewProvider(Config{
			APIKey:   "tvly-test",
			Endpoint: server.URL,
		})
		require.NoError(t, err)

		_, err = p.Search(context.Background(), "test", 5)
		require.Error(t, err)
		require.Contains(t, err.Error(), "status 429")
	})

	t.Run("handles malformed response", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{invalid json`))
		}))
		defer server.Close()

		p, err := NewProvider(Config{
			APIKey:   "tvly-test",
			Endpoint: server.URL,
		})
		require.NoError(t, err)

		_, err = p.Search(context.Background(), "test", 5)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse response")
	})

	t.Run("defaults max results", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req tavilyRequest
			json.NewDecoder(r.Body).Decode(&req)
			require.Equal(t, DefaultMaxResults, req.MaxResults)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(tavilyResponse{Results: []tavilyResult{}})
		}))
		defer server.Close()

		p, err := NewProvider(Config{
			APIKey:   "tvly-test",
			Endpoint: server.URL,
		})
		require.NoError(t, err)

		_, err = p.Search(context.Background(), "test", 0)
		require.NoError(t, err)
	})

	t.Run("context cancellation", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()

		p, err := NewProvider(Config{
			APIKey:   "tvly-test",
			Endpoint: server.URL,
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = p.Search(ctx, "test", 5)
		require.Error(t, err)
	})
}

func TestProviderImplementsInterface(t *testing.T) {
	t.Parallel()

	var _ plugin.SearchProvider = (*Provider)(nil)
}

func TestInitRegistration(t *testing.T) {
	t.Parallel()

	name, reg := plugin.GetSearchProviderRegistration()
	require.Equal(t, PluginName, name)
	require.NotNil(t, reg)
	require.NotNil(t, reg.Factory)
	require.NotNil(t, reg.ConfigSchema)
}

func TestFactoryWithoutAPIKey(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp()
	name, reg := plugin.GetSearchProviderRegistration()
	require.Equal(t, PluginName, name)

	sp, err := reg.Factory(context.Background(), app)
	require.NoError(t, err)
	require.Nil(t, sp, "should return nil when no API key configured")
}

func TestFactoryWithAPIKey(t *testing.T) {
	t.Parallel()

	app := plugin.NewApp(
		plugin.WithPluginConfig(map[string]map[string]any{
			"tavily": {
				"api_key": "tvly-test-key",
			},
		}),
	)

	_, reg := plugin.GetSearchProviderRegistration()
	sp, err := reg.Factory(context.Background(), app)
	require.NoError(t, err)
	require.NotNil(t, sp)
}
