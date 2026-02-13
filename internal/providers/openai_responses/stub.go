package openai_responses

import (
	"context"
	"fmt"

	"hyprbot/internal/providers"
)

type Client struct{}

func New() *Client { return &Client{} }

var _ providers.Provider = (*Client)(nil)

func (c *Client) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, fmt.Errorf("openai_responses provider is not enabled yet")
}
