package telegram

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters/callbackquery"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters/message"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"hyprbot/internal/crypto"
	"hyprbot/internal/metrics"
	"hyprbot/internal/queue"
	"hyprbot/internal/storage"
)

type Service struct {
	store         *storage.Store
	queue         *queue.StreamQueue
	crypto        *crypto.Manager
	rateLimiter   *queue.RateLimiter
	wizard        *wizardStore
	redis         *redis.Client
	logger        zerolog.Logger
	metrics       *metrics.Metrics
	adminCacheTTL time.Duration
	botUsername   string
	accessMode    string
	adminUserID   int64
}

type Config struct {
	Store         *storage.Store
	Queue         *queue.StreamQueue
	Crypto        *crypto.Manager
	RateLimiter   *queue.RateLimiter
	Redis         *redis.Client
	Logger        zerolog.Logger
	Metrics       *metrics.Metrics
	AdminCacheTTL time.Duration
	WizardTTL     time.Duration
	BotUsername   string
	AccessMode    string
	AdminUserID   int64
}

func NewService(cfg Config) *Service {
	m := cfg.Metrics
	if m == nil {
		m = metrics.Global()
	}
	if cfg.AdminCacheTTL <= 0 {
		cfg.AdminCacheTTL = 10 * time.Minute
	}
	if cfg.WizardTTL <= 0 {
		cfg.WizardTTL = 20 * time.Minute
	}
	return &Service{
		store:         cfg.Store,
		queue:         cfg.Queue,
		crypto:        cfg.Crypto,
		rateLimiter:   cfg.RateLimiter,
		wizard:        newWizardStore(cfg.Redis, cfg.WizardTTL),
		redis:         cfg.Redis,
		logger:        cfg.Logger,
		metrics:       m,
		adminCacheTTL: cfg.AdminCacheTTL,
		botUsername:   cfg.BotUsername,
		accessMode:    cfg.AccessMode,
		adminUserID:   cfg.AdminUserID,
	}
}

func (s *Service) Register(d *ext.Dispatcher) {
	d.AddHandler(handlers.NewCommand("help", s.help))
	d.AddHandler(handlers.NewCommand("start", s.start))
	d.AddHandler(handlers.NewCommand("menu", s.menu))
	d.AddHandler(handlers.NewCommand("setup", s.setup))
	d.AddHandler(handlers.NewCommand("status", s.status))
	d.AddHandler(handlers.NewCommand("cancel", s.cancelWizard))
	d.AddHandler(handlers.NewCommand("ask", s.ask))
	d.AddHandler(handlers.NewCommand("ai", s.ai))
	d.AddHandler(handlers.NewCommand("ai_list", s.aiList))
	d.AddHandler(handlers.NewCommand("ai_preset_add", s.aiPresetAdd))
	d.AddHandler(handlers.NewCommand("ai_preset_del", s.aiPresetDel))
	d.AddHandler(handlers.NewCommand("ai_default", s.aiDefault))
	d.AddHandler(handlers.NewCommand("llm_add", s.llmAdd))
	d.AddHandler(handlers.NewCommand("llm_list", s.llmList))
	d.AddHandler(handlers.NewCommand("llm_del", s.llmDel))
	d.AddHandler(handlers.NewCallback(callbackquery.Prefix(cbPrefix), s.onCallback))
	d.AddHandler(handlers.NewMessage(func(msg *gotgbot.Message) bool {
		return message.Private(msg) && message.Text(msg)
	}, s.privateText))
}

func (s *Service) deepLink(bot *gotgbot.Bot, param string) string {
	username := s.botUsername
	if username == "" {
		username = bot.User.Username
	}
	if strings.TrimSpace(username) == "" {
		return ""
	}
	return "https://t.me/" + username + "?start=" + url.QueryEscape(param)
}

func (s *Service) now() time.Time {
	return time.Now().UTC()
}

func (s *Service) ensureChat(ctx context.Context, msg *gotgbot.Message) {
	_ = s.store.EnsureChat(ctx, msg.Chat.Id, msg.Chat.Type, msg.Chat.Title)
}
