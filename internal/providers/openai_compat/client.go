package openai_compat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hyprbot/internal/providers"
)

type Config struct {
	BaseURL     string
	APIKey      string
	Headers     map[string]string
	Endpoint    string
	HTTPClient  *http.Client
	MaxRetries  int
	BackoffBase time.Duration
}

type Client struct {
	cfg Config
}

func New(cfg Config) *Client {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "chat_completions"
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 400 * time.Millisecond
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	return &Client{cfg: cfg}
}

var _ providers.Provider = (*Client)(nil)

func (c *Client) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	body, endpointURL, err := c.buildPayload(req)
	if err != nil {
		return providers.ChatResponse{}, err
	}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		text, retry, err := c.callOnce(ctx, endpointURL, body)
		if err == nil {
			return providers.ChatResponse{Text: text}, nil
		}
		lastErr = err
		if !retry || attempt == c.cfg.MaxRetries {
			break
		}
		backoff := c.cfg.BackoffBase * (1 << attempt)
		select {
		case <-ctx.Done():
			return providers.ChatResponse{}, ctx.Err()
		case <-time.After(backoff):
		}
	}

	return providers.ChatResponse{}, lastErr
}

func (c *Client) buildPayload(req providers.ChatRequest) ([]byte, string, error) {
	endpointURL, err := c.buildEndpointURL()
	if err != nil {
		return nil, "", err
	}

	if isResponsesEndpoint(c.cfg.Endpoint) {
		payload := map[string]any{
			"model": req.Model,
			"input": []map[string]any{
				{"role": "system", "content": req.SystemPrompt},
				{"role": "user", "content": req.UserPrompt},
			},
		}
		if req.MaxTokens > 0 {
			payload["max_output_tokens"] = req.MaxTokens
		}
		if req.Temperature > 0 {
			payload["temperature"] = req.Temperature
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, "", fmt.Errorf("marshal responses payload: %w", err)
		}
		return b, endpointURL, nil
	}

	messages := []map[string]string{}
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, map[string]string{"role": "system", "content": req.SystemPrompt})
	}
	messages = append(messages, map[string]string{"role": "user", "content": req.UserPrompt})

	payload := map[string]any{
		"model":    req.Model,
		"messages": messages,
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		payload["temperature"] = req.Temperature
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("marshal chat completion payload: %w", err)
	}
	return b, endpointURL, nil
}

func (c *Client) callOnce(ctx context.Context, endpointURL string, body []byte) (text string, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		return "", false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.cfg.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, strings.ReplaceAll(v, "{{api_key}}", c.cfg.APIKey))
	}

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", true, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", false, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return "", true, fmt.Errorf("provider temporary status %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", false, fmt.Errorf("provider status %d", resp.StatusCode)
	}

	if isResponsesEndpoint(c.cfg.Endpoint) {
		text, err := parseResponsesAPI(respBody)
		if err != nil {
			return "", false, err
		}
		return text, false, nil
	}

	text, err = parseChatCompletions(respBody)
	if err != nil {
		return "", false, err
	}
	return text, false, nil
}

func (c *Client) buildEndpointURL() (string, error) {
	base := strings.TrimSpace(c.cfg.BaseURL)
	if base == "" {
		return "", fmt.Errorf("base url is empty")
	}
	if strings.HasSuffix(base, "/chat/completions") || strings.HasSuffix(base, "/responses") {
		return base, nil
	}

	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	path := strings.TrimSuffix(u.Path, "/")
	if isResponsesEndpoint(c.cfg.Endpoint) {
		u.Path = path + "/responses"
	} else {
		u.Path = path + "/chat/completions"
	}
	return u.String(), nil
}

func parseChatCompletions(body []byte) (string, error) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
			Text string `json:"text"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("decode chat completion response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty choices in chat completion response")
	}
	if resp.Choices[0].Text != "" {
		return resp.Choices[0].Text, nil
	}
	if content := anyToText(resp.Choices[0].Message.Content); strings.TrimSpace(content) != "" {
		return content, nil
	}
	return "", fmt.Errorf("missing message content in chat completion response")
}

func parseResponsesAPI(body []byte) (string, error) {
	var resp struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("decode responses api response: %w", err)
	}
	if strings.TrimSpace(resp.OutputText) != "" {
		return resp.OutputText, nil
	}
	if len(resp.Output) > 0 && len(resp.Output[0].Content) > 0 && strings.TrimSpace(resp.Output[0].Content[0].Text) != "" {
		return resp.Output[0].Content[0].Text, nil
	}
	return "", fmt.Errorf("missing output text in responses api response")
}

func anyToText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			if m, ok := item.(map[string]any); ok {
				if txt, ok := m["text"].(string); ok {
					parts = append(parts, txt)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func isResponsesEndpoint(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "responses" || v == "/v1/responses"
}
