package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/rs/zerolog"

	"hyprbot/internal/crypto"
	"hyprbot/internal/metrics"
	"hyprbot/internal/providers"
	"hyprbot/internal/providers/registry"
	"hyprbot/internal/queue"
	"hyprbot/internal/storage"
)

type Worker struct {
	bot             *gotgbot.Bot
	store           *storage.Store
	queue           *queue.StreamQueue
	crypto          *crypto.Manager
	httpClient      *http.Client
	providerRetries int
	backoffBase     time.Duration
	maxJobRetries   int
	logger          zerolog.Logger
	metrics         *metrics.Metrics
}

type Config struct {
	Bot             *gotgbot.Bot
	Store           *storage.Store
	Queue           *queue.StreamQueue
	Crypto          *crypto.Manager
	HTTPClient      *http.Client
	ProviderRetries int
	BackoffBase     time.Duration
	MaxJobRetries   int
	Logger          zerolog.Logger
	Metrics         *metrics.Metrics
}

func New(cfg Config) *Worker {
	m := cfg.Metrics
	if m == nil {
		m = metrics.Global()
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 400 * time.Millisecond
	}
	if cfg.MaxJobRetries < 0 {
		cfg.MaxJobRetries = 0
	}
	return &Worker{
		bot:             cfg.Bot,
		store:           cfg.Store,
		queue:           cfg.Queue,
		crypto:          cfg.Crypto,
		httpClient:      cfg.HTTPClient,
		providerRetries: cfg.ProviderRetries,
		backoffBase:     cfg.BackoffBase,
		maxJobRetries:   cfg.MaxJobRetries,
		logger:          cfg.Logger,
		metrics:         m,
	}
}

func (w *Worker) Start(ctx context.Context, concurrency int) error {
	if err := w.queue.EnsureGroup(ctx); err != nil {
		return err
	}
	if concurrency < 1 {
		concurrency = 1
	}

	wg := sync.WaitGroup{}
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			w.consumeLoop(ctx, slot)
		}(i)
	}

	<-ctx.Done()
	wg.Wait()
	return nil
}

func (w *Worker) consumeLoop(ctx context.Context, slot int) {
	log := w.logger.With().Int("slot", slot).Logger()
	for {
		if err := ctx.Err(); err != nil {
			return
		}

		messages, err := w.queue.Read(ctx, 1)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error().Err(err).Msg("failed to read queue")
			time.Sleep(1 * time.Second)
			continue
		}
		if len(messages) == 0 {
			continue
		}

		for _, msg := range messages {
			err := w.processJob(ctx, msg.Job)
			if err == nil {
				w.metrics.ProcessedJobs.Inc()
				if ackErr := w.queue.Ack(ctx, msg.ID); ackErr != nil {
					log.Error().Err(ackErr).Str("msg_id", msg.ID).Msg("failed to ack message")
				}
				continue
			}

			w.metrics.FailedJobs.Inc()
			log.Error().Err(err).Str("job_id", msg.Job.JobID).Int("attempt", msg.Job.Attempts).Msg("job failed")

			if msg.Job.Attempts < w.maxJobRetries {
				msg.Job.Attempts++
				if _, enqueueErr := w.queue.Enqueue(ctx, msg.Job); enqueueErr != nil {
					log.Error().Err(enqueueErr).Str("job_id", msg.Job.JobID).Msg("failed to re-enqueue failed job")
					continue
				}
				if ackErr := w.queue.Ack(ctx, msg.ID); ackErr != nil {
					log.Error().Err(ackErr).Str("msg_id", msg.ID).Msg("failed to ack after re-enqueue")
				}
				continue
			}

			_ = w.sendError(ctx, msg.Job.ChatID, msg.Job.MessageID, "LLM provider error. Please try again later.")
			if ackErr := w.queue.Ack(ctx, msg.ID); ackErr != nil {
				log.Error().Err(ackErr).Str("msg_id", msg.ID).Msg("failed to ack terminal failed message")
			}
		}
	}
}

func (w *Worker) processJob(ctx context.Context, job queue.AskJob) error {
	presetWithProvider, err := w.resolvePreset(ctx, job.ChatID, job.PresetName)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			_ = w.sendError(ctx, job.ChatID, job.MessageID, "Preset not found. Configure /ai_default or use /ai <preset>.")
			return nil
		}
		return err
	}

	apiKey, err := w.decryptOptional(presetWithProvider.Provider.EncAPIKey)
	if err != nil {
		return fmt.Errorf("decrypt api key: %w", err)
	}
	headers := map[string]string{}
	if raw, err := w.decryptOptional(presetWithProvider.Provider.EncHeadersJSON); err != nil {
		return fmt.Errorf("decrypt headers: %w", err)
	} else if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &headers); err != nil {
			return fmt.Errorf("parse headers json: %w", err)
		}
	}

	providerCfg := map[string]any{}
	if strings.TrimSpace(presetWithProvider.Provider.ConfigJSON) != "" {
		if err := json.Unmarshal([]byte(presetWithProvider.Provider.ConfigJSON), &providerCfg); err != nil {
			return fmt.Errorf("parse provider config: %w", err)
		}
	}

	p, err := registry.Build(registry.BuildOptions{
		Kind:        presetWithProvider.Provider.Kind,
		BaseURL:     presetWithProvider.Provider.BaseURL,
		APIKey:      apiKey,
		Headers:     headers,
		Config:      providerCfg,
		HTTPClient:  w.httpClient,
		MaxRetries:  w.providerRetries,
		BackoffBase: w.backoffBase,
	})
	if err != nil {
		return fmt.Errorf("build provider: %w", err)
	}

	params := presetParams{MaxTokens: 1024, Temperature: 0.7, AllowTools: false}
	if raw := strings.TrimSpace(presetWithProvider.Preset.ParamsJSON); raw != "" {
		_ = json.Unmarshal([]byte(raw), &params)
	}

	resp, err := p.Chat(ctx, providers.ChatRequest{
		Model:        presetWithProvider.Preset.Model,
		SystemPrompt: presetWithProvider.Preset.SystemPrompt,
		UserPrompt:   job.Prompt,
		MaxTokens:    params.MaxTokens,
		Temperature:  params.Temperature,
		AllowTools:   params.AllowTools,
	})
	if err != nil {
		return fmt.Errorf("provider chat: %w", err)
	}

	text := strings.TrimSpace(resp.Text)
	if text == "" {
		text = "Provider returned an empty response."
	}
	if len([]rune(text)) > 4000 {
		r := []rune(text)
		text = string(r[:4000])
	}

	sendOpts := &gotgbot.SendMessageOpts{}
	if job.MessageID > 0 {
		sendOpts.ReplyParameters = &gotgbot.ReplyParameters{MessageId: job.MessageID}
	}
	_, err = w.bot.SendMessageWithContext(ctx, job.ChatID, text, sendOpts)
	if err != nil {
		return fmt.Errorf("send telegram response: %w", err)
	}
	return nil
}

func (w *Worker) resolvePreset(ctx context.Context, chatID int64, presetName string) (storage.PresetWithProvider, error) {
	if strings.TrimSpace(presetName) == "" {
		return w.store.GetDefaultPresetWithProvider(ctx, chatID)
	}
	return w.store.GetPresetWithProviderByName(ctx, chatID, presetName)
}

func (w *Worker) decryptOptional(raw *string) (string, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return "", nil
	}
	return w.crypto.UnmarshalEncryptedString(*raw)
}

func (w *Worker) sendError(ctx context.Context, chatID, replyTo int64, text string) error {
	opts := &gotgbot.SendMessageOpts{}
	if replyTo > 0 {
		opts.ReplyParameters = &gotgbot.ReplyParameters{MessageId: replyTo}
	}
	_, err := w.bot.SendMessageWithContext(ctx, chatID, text, opts)
	return err
}

type presetParams struct {
	MaxTokens   int     `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
	AllowTools  bool    `json:"allow_tools"`
}
