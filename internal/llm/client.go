package llm

import (
	"context"
	"errors"
	"net/http"

	openai "github.com/sashabaranov/go-openai"
)

// Client wraps the OpenAI SDK with our request shape. Model is passed as a
// string (e.g. "gpt-5-mini") to every request so we can swap models without
// changing method signatures.
type Client struct {
	sdk   *openai.Client
	model string
}

// Options lets callers (mostly tests) override the underlying HTTP client
// and API base URL — the test suite spins up httptest.NewServer and points
// BaseURL at it.
type Options struct {
	HTTPClient *http.Client
	BaseURL    string // empty = default OpenAI endpoint
}

// NewClient constructs a Client for the OpenAI API. Returns an error if
// apiKey is empty.
func NewClient(ctx context.Context, apiKey, model string, opts Options) (*Client, error) {
	if apiKey == "" {
		return nil, errors.New("missing OPENAI_API_KEY")
	}
	cfg := openai.DefaultConfig(apiKey)
	if opts.BaseURL != "" {
		cfg.BaseURL = opts.BaseURL
	}
	if opts.HTTPClient != nil {
		cfg.HTTPClient = opts.HTTPClient
	}
	return &Client{
		sdk:   openai.NewClientWithConfig(cfg),
		model: model,
	}, nil
}
