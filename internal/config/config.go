package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	ModeAll     = "ALL"
	ModeWebhook = "WEBHOOK"
	ModeWorker  = "WORKER"

	AccessModePublic  = "public"
	AccessModePrivate = "private"
)

var (
	ErrMissingBotToken    = errors.New("BOT_TOKEN is required")
	ErrMissingAdminUserID = errors.New("ADMIN_USER_ID is required and must be > 0")
	ErrInvalidAccessMode  = errors.New("BOT_ACCESS_MODE must be 'public' or 'private'")
	ErrMissingDatabaseDSN = errors.New("DB_DSN is required")
	ErrMissingMasterKey   = errors.New("at least one master key is required")
)

type Config struct {
	BotToken      string
	AppMode       string
	BotAccessMode string
	AdminUserID   int64

	BotUsername string

	DevPolling bool

	Webhook WebhookConfig
	Redis   RedisConfig
	DB      DBConfig
	Worker  WorkerConfig
	HTTP    HTTPConfig
	Rate    RateConfig
	Crypto  CryptoConfig
	Log     LogConfig
}

type WebhookConfig struct {
	ListenAddr     string
	PublicURL      string
	SecretPath     string
	SecretToken    string
	HealthPath     string
	MetricsPath    string
	WebhookTimeout time.Duration
}

type RedisConfig struct {
	Addr          string
	Password      string
	DB            int
	QueueStream   string
	QueueGroup    string
	QueueBlock    time.Duration
	UpdateTTL     time.Duration
	WizardTTL     time.Duration
	AdminCacheTTL time.Duration
}

type DBConfig struct {
	Driver      string
	DSN         string
	AutoMigrate bool
}

type WorkerConfig struct {
	Concurrency  int
	ConsumerName string
	MaxRetries   int
}

type HTTPConfig struct {
	ClientTimeout time.Duration
	MaxRetries    int
	BackoffBase   time.Duration
}

type RateConfig struct {
	PerHour int64
}

type CryptoConfig struct {
	CurrentKeyID string
	Keys         map[string][]byte
}

type LogConfig struct {
	Level string
}

func Load() (*Config, error) {
	cfg := &Config{
		BotToken:      mustEnv("BOT_TOKEN", ""),
		AppMode:       strings.ToUpper(mustEnv("APP_MODE", ModeAll)),
		BotAccessMode: strings.ToLower(mustEnv("BOT_ACCESS_MODE", AccessModePublic)),
		AdminUserID:   mustInt64("ADMIN_USER_ID", 0),
		DevPolling:    mustBool("DEV_POLLING", false),
		Webhook: WebhookConfig{
			ListenAddr:     mustEnv("WEBHOOK_LISTEN_ADDR", ":8080"),
			PublicURL:      mustEnv("WEBHOOK_URL", ""),
			SecretPath:     strings.Trim(mustEnv("WEBHOOK_SECRET_PATH", "telegram"), "/"),
			SecretToken:    mustEnv("WEBHOOK_SECRET_TOKEN", ""),
			HealthPath:     mustEnv("HEALTH_PATH", "/healthz"),
			MetricsPath:    mustEnv("METRICS_PATH", "/metrics"),
			WebhookTimeout: mustDuration("WEBHOOK_TIMEOUT", 8*time.Second),
		},
		Redis: RedisConfig{
			Addr:          mustEnv("REDIS_ADDR", "127.0.0.1:6379"),
			Password:      mustEnv("REDIS_PASSWORD", ""),
			DB:            mustInt("REDIS_DB", 0),
			QueueStream:   mustEnv("QUEUE_STREAM", "hyprbot:jobs"),
			QueueGroup:    mustEnv("QUEUE_GROUP", "hyprbot-workers"),
			QueueBlock:    mustDuration("QUEUE_BLOCK", 5*time.Second),
			UpdateTTL:     mustDuration("UPDATE_DEDUPE_TTL", 6*time.Hour),
			WizardTTL:     mustDuration("WIZARD_TTL", 20*time.Minute),
			AdminCacheTTL: mustDuration("ADMIN_CACHE_TTL", 10*time.Minute),
		},
		DB: DBConfig{
			Driver:      strings.ToLower(mustEnv("DB_DRIVER", "postgres")),
			DSN:         mustEnv("DB_DSN", "postgres://postgres:postgres@postgres:5432/hyprbot?sslmode=disable"),
			AutoMigrate: mustBool("AUTO_MIGRATE", true),
		},
		Worker: WorkerConfig{
			Concurrency:  mustInt("WORKER_CONCURRENCY", 4),
			ConsumerName: mustEnv("WORKER_CONSUMER_NAME", hostnameOr("worker")),
			MaxRetries:   mustInt("WORKER_MAX_RETRIES", 3),
		},
		HTTP: HTTPConfig{
			ClientTimeout: mustDuration("HTTP_TIMEOUT", 30*time.Second),
			MaxRetries:    mustInt("HTTP_MAX_RETRIES", 2),
			BackoffBase:   mustDuration("HTTP_BACKOFF_BASE", 400*time.Millisecond),
		},
		Rate: RateConfig{
			PerHour: int64(mustInt("RATE_LIMIT_PER_HOUR", 30)),
		},
		Log: LogConfig{
			Level: strings.ToLower(mustEnv("LOG_LEVEL", "info")),
		},
	}

	if cfg.BotToken == "" {
		return nil, ErrMissingBotToken
	}
	if cfg.BotAccessMode != AccessModePublic && cfg.BotAccessMode != AccessModePrivate {
		return nil, ErrInvalidAccessMode
	}
	if cfg.BotAccessMode == AccessModePrivate && cfg.AdminUserID <= 0 {
		return nil, ErrMissingAdminUserID
	}
	if cfg.DB.DSN == "" {
		return nil, ErrMissingDatabaseDSN
	}
	if cfg.AppMode != ModeAll && cfg.AppMode != ModeWebhook && cfg.AppMode != ModeWorker {
		return nil, fmt.Errorf("unsupported APP_MODE %q", cfg.AppMode)
	}

	cc, err := loadCryptoConfig()
	if err != nil {
		return nil, err
	}
	cfg.Crypto = cc

	return cfg, nil
}

func loadCryptoConfig() (CryptoConfig, error) {
	keysB64 := map[string]string{}

	if raw := mustEnv("MASTER_KEYS_JSON", ""); raw != "" {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return CryptoConfig{}, fmt.Errorf("parse MASTER_KEYS_JSON: %w", err)
		}
		for id, val := range parsed {
			if strings.TrimSpace(id) == "" || strings.TrimSpace(val) == "" {
				continue
			}
			keysB64[id] = val
		}
	}

	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k, v := parts[0], parts[1]
		if !strings.HasPrefix(k, "MASTER_KEY_") || !strings.HasSuffix(k, "_B64") {
			continue
		}
		if k == "MASTER_KEY_B64" {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(k, "MASTER_KEY_"), "_B64")
		if id == "" || v == "" {
			continue
		}
		keysB64[id] = v
	}

	current := mustEnv("MASTER_KEY_CURRENT_ID", "")
	if singleton := mustEnv("MASTER_KEY_B64", ""); singleton != "" {
		if current == "" {
			current = "default"
		}
		keysB64[current] = singleton
	}

	if len(keysB64) == 0 {
		return CryptoConfig{}, ErrMissingMasterKey
	}

	keys := make(map[string][]byte, len(keysB64))
	for id, b64 := range keysB64 {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return CryptoConfig{}, fmt.Errorf("decode master key %q: %w", id, err)
		}
		if len(raw) != 32 {
			return CryptoConfig{}, fmt.Errorf("master key %q must be 32 bytes after base64 decode", id)
		}
		keys[id] = raw
	}

	if current == "" {
		for id := range keys {
			current = id
			break
		}
	}
	if _, ok := keys[current]; !ok {
		return CryptoConfig{}, fmt.Errorf("MASTER_KEY_CURRENT_ID=%q does not exist in provided keys", current)
	}

	return CryptoConfig{
		CurrentKeyID: current,
		Keys:         keys,
	}, nil
}

func mustEnv(key string, def string) string {
	if v := os.Getenv(key); v != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func mustInt(key string, def int) int {
	v := mustEnv(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func mustInt64(key string, def int64) int64 {
	v := mustEnv(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func mustBool(key string, def bool) bool {
	v := mustEnv(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func mustDuration(key string, def time.Duration) time.Duration {
	v := mustEnv(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func hostnameOr(def string) string {
	h, err := os.Hostname()
	if err != nil || strings.TrimSpace(h) == "" {
		return def
	}
	return h
}
