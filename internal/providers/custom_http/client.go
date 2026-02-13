package custom_http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"hyprbot/internal/providers"
)

type Config struct {
	URL          string
	APIKey       string
	Headers      map[string]string
	BodyTemplate string
	Method       string
	HTTPClient   *http.Client
	MaxRetries   int
	BackoffBase  time.Duration
}

type Client struct {
	cfg Config
}

func New(cfg Config) *Client {
	if cfg.Method == "" {
		cfg.Method = http.MethodPost
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
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
	body, err := c.renderBody(req)
	if err != nil {
		return providers.ChatResponse{}, err
	}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		text, retry, err := c.callOnce(ctx, body)
		if err == nil {
			return providers.ChatResponse{Text: text}, nil
		}
		lastErr = err
		if !retry || attempt == c.cfg.MaxRetries {
			break
		}
		select {
		case <-ctx.Done():
			return providers.ChatResponse{}, ctx.Err()
		case <-time.After(c.cfg.BackoffBase * (1 << attempt)):
		}
	}

	return providers.ChatResponse{}, lastErr
}

func (c *Client) renderBody(req providers.ChatRequest) ([]byte, error) {
	if strings.TrimSpace(c.cfg.BodyTemplate) == "" {
		payload := map[string]any{
			"model":         req.Model,
			"system_prompt": req.SystemPrompt,
			"prompt":        req.UserPrompt,
			"max_tokens":    req.MaxTokens,
			"temperature":   req.Temperature,
			"allow_tools":   req.AllowTools,
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal custom payload: %w", err)
		}
		return b, nil
	}

	tpl, err := template.New("custom_http_body").Option("missingkey=zero").Parse(c.cfg.BodyTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse body template: %w", err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, map[string]any{
		"Model":        req.Model,
		"SystemPrompt": req.SystemPrompt,
		"UserPrompt":   req.UserPrompt,
		"MaxTokens":    req.MaxTokens,
		"Temperature":  req.Temperature,
		"AllowTools":   req.AllowTools,
		"APIKey":       c.cfg.APIKey,
	}); err != nil {
		return nil, fmt.Errorf("execute body template: %w", err)
	}
	return buf.Bytes(), nil
}

func (c *Client) callOnce(ctx context.Context, body []byte) (text string, retry bool, err error) {
	if strings.TrimSpace(c.cfg.URL) == "" {
		return "", false, fmt.Errorf("custom http url is empty")
	}
	req, err := http.NewRequestWithContext(ctx, c.cfg.Method, c.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return "", false, fmt.Errorf("build custom request: %w", err)
	}
	if len(c.cfg.Headers) == 0 {
		req.Header.Set("Content-Type", "application/json")
	} else {
		for k, v := range c.cfg.Headers {
			req.Header.Set(k, strings.ReplaceAll(v, "{{api_key}}", c.cfg.APIKey))
		}
	}

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", true, fmt.Errorf("custom request failed: %w", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", false, fmt.Errorf("read custom response: %w", err)
	}

	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return "", true, fmt.Errorf("custom provider temporary status %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", false, fmt.Errorf("custom provider status %d", resp.StatusCode)
	}

	text, err = extractText(b)
	if err != nil {
		return "", false, err
	}
	return text, false, nil
}

func extractText(body []byte) (string, error) {
	var simple map[string]any
	if err := json.Unmarshal(body, &simple); err != nil {
		trimmed := strings.TrimSpace(string(body))
		if trimmed != "" {
			return trimmed, nil
		}
		return "", fmt.Errorf("decode custom response: %w", err)
	}

	for _, key := range []string{"text", "response", "answer", "output_text"} {
		if v, ok := simple[key].(string); ok && strings.TrimSpace(v) != "" {
			return v, nil
		}
	}

	if choices, ok := simple["choices"].([]any); ok && len(choices) > 0 {
		if c0, ok := choices[0].(map[string]any); ok {
			if msg, ok := c0["message"].(map[string]any); ok {
				if content, ok := msg["content"].(string); ok && strings.TrimSpace(content) != "" {
					return content, nil
				}
			}
			if text, ok := c0["text"].(string); ok && strings.TrimSpace(text) != "" {
				return text, nil
			}
		}
	}

	if out, ok := simple["output"].([]any); ok && len(out) > 0 {
		if o0, ok := out[0].(map[string]any); ok {
			if content, ok := o0["content"].([]any); ok && len(content) > 0 {
				if c0, ok := content[0].(map[string]any); ok {
					if text, ok := c0["text"].(string); ok && strings.TrimSpace(text) != "" {
						return text, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("custom response does not contain text field")
}
