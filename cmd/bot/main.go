package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"hyprbot/internal/config"
	"hyprbot/internal/crypto"
	"hyprbot/internal/metrics"
	"hyprbot/internal/queue"
	"hyprbot/internal/storage"
	"hyprbot/internal/telegram"
	"hyprbot/internal/worker"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	setupLogger(cfg.Log.Level)
	log.Info().
		Str("mode", cfg.AppMode).
		Str("access_mode", cfg.BotAccessMode).
		Bool("dev_polling", cfg.DevPolling).
		Int64("admin_user_id", cfg.AdminUserID).
		Msg("starting hyprbot")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	store, err := storage.Open(ctx, cfg.DB.Driver, cfg.DB.DSN, cfg.DB.AutoMigrate, "migrations")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize storage")
	}
	defer store.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal().Err(err).Msg("failed to connect redis")
	}
	defer rdb.Close()

	cryptoManager, err := crypto.NewManager(cfg.Crypto.CurrentKeyID, cfg.Crypto.Keys)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize crypto manager")
	}

	bot, err := gotgbot.NewBot(cfg.BotToken, nil)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create telegram bot")
	}
	log.Info().Str("bot_username", bot.User.Username).Int64("bot_id", bot.User.Id).Msg("telegram bot initialized")

	m := metrics.Global()
	jobQueue := queue.NewStreamQueue(rdb, cfg.Redis.QueueStream, cfg.Redis.QueueGroup, cfg.Worker.ConsumerName, cfg.Redis.QueueBlock)

	errCh := make(chan error, 4)
	var updater *ext.Updater
	var httpServer *http.Server
	var webhookHandler http.HandlerFunc
	var webhookRoute string
	logTelegramErr := func(err error) {
		log.Error().Str("component", "telegram").Msg(sanitizeTelegramErr(err, cfg.BotToken))
	}

	runPolling := cfg.DevPolling && cfg.AppMode != config.ModeWorker
	runWebhook := !runPolling && (cfg.AppMode == config.ModeWebhook || cfg.AppMode == config.ModeAll)
	runIngress := runPolling || runWebhook
	if runIngress {
		allowedUserID := int64(0)
		if cfg.BotAccessMode == config.AccessModePrivate {
			allowedUserID = cfg.AdminUserID
		}
		dispatcher := ext.NewDispatcher(&ext.DispatcherOpts{
			MaxRoutines:      100,
			UnhandledErrFunc: logTelegramErr,
			Processor: telegram.Processor{
				Dedupe:        queue.NewUpdateDeduplicator(rdb, cfg.Redis.UpdateTTL),
				Metrics:       m,
				Logger:        log.Logger,
				AllowedUserID: allowedUserID,
			},
		})
		service := telegram.NewService(telegram.Config{
			Store:         store,
			Queue:         jobQueue,
			Crypto:        cryptoManager,
			RateLimiter:   queue.NewRateLimiter(rdb, cfg.Rate.PerHour),
			Redis:         rdb,
			Logger:        log.Logger,
			Metrics:       m,
			AdminCacheTTL: cfg.Redis.AdminCacheTTL,
			WizardTTL:     cfg.Redis.WizardTTL,
			BotUsername:   bot.User.Username,
			AccessMode:    cfg.BotAccessMode,
			AdminUserID:   cfg.AdminUserID,
		})
		service.Register(dispatcher)
		updater = ext.NewUpdater(dispatcher, &ext.UpdaterOpts{
			UnhandledErrFunc: logTelegramErr,
		})

		if runPolling {
			if err := updater.StartPolling(bot, &ext.PollingOpts{
				EnableWebhookDeletion: true,
				DropPendingUpdates:    true,
				GetUpdatesOpts: &gotgbot.GetUpdatesOpts{
					Timeout: 50,
					RequestOpts: &gotgbot.RequestOpts{
						Timeout: 60 * time.Second,
					},
				},
			}); err != nil {
				log.Fatal().Err(err).Msg("failed to start polling")
			}
			log.Info().Msg("polling mode started")
		} else if runWebhook {
			path := strings.Trim(cfg.Webhook.SecretPath, "/")
			if path == "" {
				path = "telegram"
			}
			if cfg.Webhook.PublicURL == "" {
				log.Fatal().Msg("WEBHOOK_URL is required in webhook mode")
			}
			if err := updater.AddWebhook(bot, path, &ext.AddWebhookOpts{SecretToken: cfg.Webhook.SecretToken}); err != nil {
				log.Fatal().Err(err).Msg("failed to configure webhook handler")
			}

			webhookURL := strings.TrimSuffix(cfg.Webhook.PublicURL, "/") + "/" + path
			if _, err := bot.SetWebhook(webhookURL, &gotgbot.SetWebhookOpts{
				DropPendingUpdates: false,
				SecretToken:        cfg.Webhook.SecretToken,
			}); err != nil {
				log.Fatal().Err(err).Msg("failed to set telegram webhook")
			}
			log.Info().Str("webhook_url", webhookURL).Msg("webhook registered")
			webhookRoute = "/" + path
			webhookHandler = updater.GetHandlerFunc("/")
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc(cfg.Webhook.HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle(cfg.Webhook.MetricsPath, promhttp.Handler())
	if webhookHandler != nil && webhookRoute != "" {
		mux.HandleFunc(webhookRoute, webhookHandler)
	}
	httpServer = &http.Server{
		Addr:              cfg.Webhook.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.Webhook.WebhookTimeout,
	}
	go func() {
		log.Info().Str("addr", cfg.Webhook.ListenAddr).Msg("http server started")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()

	if cfg.AppMode == config.ModeWorker || cfg.AppMode == config.ModeAll {
		w := worker.New(worker.Config{
			Bot:             bot,
			Store:           store,
			Queue:           jobQueue,
			Crypto:          cryptoManager,
			ProviderRetries: cfg.HTTP.MaxRetries,
			BackoffBase:     cfg.HTTP.BackoffBase,
			MaxJobRetries:   cfg.Worker.MaxRetries,
			Logger:          log.Logger,
			Metrics:         m,
		})
		go func() {
			if err := w.Start(ctx, cfg.Worker.Concurrency); err != nil && ctx.Err() == nil {
				errCh <- fmt.Errorf("worker failed: %w", err)
			}
		}()
		log.Info().Int("concurrency", cfg.Worker.Concurrency).Msg("worker started")
	}

	select {
	case <-ctx.Done():
		log.Info().Msg("shutdown signal received")
	case err := <-errCh:
		log.Error().Err(err).Msg("runtime error")
		cancel()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if updater != nil {
		if err := updater.Stop(); err != nil {
			log.Error().Err(err).Msg("failed to stop updater")
		}
	}
	if httpServer != nil {
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("failed to stop http server")
		}
	}

	log.Info().Msg("stopped")
}

func setupLogger(level string) {
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.SetGlobalLevel(parseLogLevel(level))
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
}

func parseLogLevel(level string) zerolog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return zerolog.DebugLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}

func sanitizeTelegramErr(err error, token string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if strings.TrimSpace(token) == "" {
		return msg
	}

	msg = strings.ReplaceAll(msg, token, "<redacted-token>")
	if idx := strings.Index(token, ":"); idx > 0 {
		botID := token[:idx]
		msg = strings.ReplaceAll(msg, "/bot"+botID+":", "/bot<redacted>:")
		msg = strings.ReplaceAll(msg, "bot"+botID+"/", "bot<redacted>/")
	}
	return msg
}
