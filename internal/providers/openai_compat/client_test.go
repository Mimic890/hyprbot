package openai_compat

import (
	"encoding/json"
	"testing"

	"hyprbot/internal/providers"
)

func TestBuildPayloadChatCompletions(t *testing.T) {
	c := New(Config{BaseURL: "https://api.x.ai/v1", Endpoint: "chat_completions"})

	body, endpoint, err := c.buildPayload(providers.ChatRequest{
		Model:        "grok-beta",
		SystemPrompt: "You are concise",
		UserPrompt:   "hello",
		MaxTokens:    123,
		Temperature:  0.4,
	})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if endpoint != "https://api.x.ai/v1/chat/completions" {
		t.Fatalf("unexpected endpoint %q", endpoint)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["model"] != "grok-beta" {
		t.Fatalf("expected model grok-beta, got %#v", payload["model"])
	}
	if _, ok := payload["messages"]; !ok {
		t.Fatalf("messages missing in payload")
	}
}

func TestBuildPayloadResponsesEndpoint(t *testing.T) {
	c := New(Config{BaseURL: "https://api.openai.com/v1", Endpoint: "responses"})

	_, endpoint, err := c.buildPayload(providers.ChatRequest{Model: "gpt-4.1", UserPrompt: "hello"})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if endpoint != "https://api.openai.com/v1/responses" {
		t.Fatalf("unexpected endpoint %q", endpoint)
	}
}
