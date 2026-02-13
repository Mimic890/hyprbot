package registry

import (
	"fmt"
	"net/http"
	"time"

	"hyprbot/internal/providers"
	"hyprbot/internal/providers/custom_http"
	"hyprbot/internal/providers/openai_compat"
)

type BuildOptions struct {
	Kind        string
	BaseURL     string
	APIKey      string
	Headers     map[string]string
	Config      map[string]any
	HTTPClient  *http.Client
	MaxRetries  int
	BackoffBase time.Duration
}

func Build(opts BuildOptions) (providers.Provider, error) {
	if opts.Config == nil {
		opts.Config = map[string]any{}
	}
	switch opts.Kind {
	case "openai_compat", "openai-compatible", "openai":
		endpoint := "chat_completions"
		if v, ok := opts.Config["endpoint"].(string); ok && v != "" {
			endpoint = v
		}
		return openai_compat.New(openai_compat.Config{
			BaseURL:     opts.BaseURL,
			APIKey:      opts.APIKey,
			Headers:     opts.Headers,
			Endpoint:    endpoint,
			HTTPClient:  opts.HTTPClient,
			MaxRetries:  opts.MaxRetries,
			BackoffBase: opts.BackoffBase,
		}), nil

	case "custom_http", "custom-http":
		bodyTemplate := ""
		if v, ok := opts.Config["body_template"].(string); ok {
			bodyTemplate = v
		}
		method := "POST"
		if v, ok := opts.Config["method"].(string); ok && v != "" {
			method = v
		}
		return custom_http.New(custom_http.Config{
			URL:          opts.BaseURL,
			APIKey:       opts.APIKey,
			Headers:      opts.Headers,
			BodyTemplate: bodyTemplate,
			Method:       method,
			HTTPClient:   opts.HTTPClient,
			MaxRetries:   opts.MaxRetries,
			BackoffBase:  opts.BackoffBase,
		}), nil

	default:
		return nil, fmt.Errorf("unsupported provider kind %q", opts.Kind)
	}
}
