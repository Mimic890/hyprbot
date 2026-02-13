package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/redis/go-redis/v9"

	"hyprbot/internal/queue"
	"hyprbot/internal/storage"
)

var providerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func (s *Service) help(b *gotgbot.Bot, ctx *ext.Context) error {
	text := strings.Join([]string{
		"Commands:",
		"/help",
		"/ask <text>",
		"/ai <preset> <text>",
		"/ai_list",
		"Admin:",
		"/ai_preset_add <name> <provider> <model> <system_prompt...>",
		"/ai_preset_del <name>",
		"/ai_default <name>",
		"/llm_add",
		"/llm_list",
		"/llm_del <name>",
		"Private wizard:",
		"/start llmadd_<chat_id>",
		"/cancel",
	}, "\n")
	return s.reply(ctx, b, text)
}

func (s *Service) start(b *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.EffectiveChat == nil {
		return nil
	}
	args := ctx.Args()
	if ctx.EffectiveChat.Type == "private" && len(args) > 1 && strings.HasPrefix(args[1], "llmadd_") {
		chatID, err := strconv.ParseInt(strings.TrimPrefix(args[1], "llmadd_"), 10, 64)
		if err != nil {
			return s.reply(ctx, b, "Invalid deep-link payload.")
		}
		return s.beginLLMAddWizard(ctx, b, chatID)
	}
	return s.help(b, ctx)
}

func (s *Service) cancelWizard(b *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.EffectiveChat == nil || ctx.EffectiveUser == nil || ctx.EffectiveChat.Type != "private" {
		return nil
	}
	if err := s.wizard.Clear(context.Background(), ctx.EffectiveUser.Id); err != nil {
		return s.reply(ctx, b, "Failed to cancel wizard right now.")
	}
	return s.reply(ctx, b, "Wizard canceled.")
}

func (s *Service) ask(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.EffectiveMessage
	if msg == nil || ctx.EffectiveChat == nil {
		return nil
	}
	prompt := strings.TrimSpace(commandRemainder(msg.GetText()))
	if prompt == "" {
		return s.reply(ctx, b, "Usage: /ask <text>")
	}

	if !s.allowRate(ctx.EffectiveChat.Id, userID(ctx), b, ctx) {
		return nil
	}

	s.ensureChat(context.Background(), msg)
	job := queue.AskJob{
		ChatID:    ctx.EffectiveChat.Id,
		ChatType:  ctx.EffectiveChat.Type,
		UserID:    userID(ctx),
		MessageID: msg.MessageId,
		Prompt:    prompt,
	}
	if _, err := s.queue.Enqueue(context.Background(), job); err != nil {
		s.logger.Error().Err(err).Msg("failed to enqueue /ask job")
		return s.reply(ctx, b, "Queue is unavailable right now.")
	}
	s.metrics.EnqueuedJobs.Inc()
	return s.reply(ctx, b, "Accepted. Processing in queue.")
}

func (s *Service) ai(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.EffectiveMessage
	if msg == nil || ctx.EffectiveChat == nil {
		return nil
	}
	rest := strings.TrimSpace(commandRemainder(msg.GetText()))
	preset, prompt := splitFirstWord(rest)
	if preset == "" || prompt == "" {
		return s.reply(ctx, b, "Usage: /ai <preset> <text>")
	}

	if !s.allowRate(ctx.EffectiveChat.Id, userID(ctx), b, ctx) {
		return nil
	}

	s.ensureChat(context.Background(), msg)
	job := queue.AskJob{
		ChatID:     ctx.EffectiveChat.Id,
		ChatType:   ctx.EffectiveChat.Type,
		UserID:     userID(ctx),
		MessageID:  msg.MessageId,
		Prompt:     prompt,
		PresetName: preset,
	}
	if _, err := s.queue.Enqueue(context.Background(), job); err != nil {
		s.logger.Error().Err(err).Msg("failed to enqueue /ai job")
		return s.reply(ctx, b, "Queue is unavailable right now.")
	}
	s.metrics.EnqueuedJobs.Inc()
	return s.reply(ctx, b, "Accepted. Processing in queue.")
}

func (s *Service) aiList(b *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.EffectiveChat == nil {
		return nil
	}
	presets, err := s.store.ListPresets(context.Background(), ctx.EffectiveChat.Id)
	if err != nil {
		s.logger.Error().Err(err).Msg("list presets failed")
		return s.reply(ctx, b, "Failed to load presets.")
	}
	if len(presets) == 0 {
		return s.reply(ctx, b, "No presets configured.")
	}
	defaultName, _ := s.store.GetDefaultPresetName(context.Background(), ctx.EffectiveChat.Id)

	lines := []string{"Presets:"}
	for _, p := range presets {
		line := fmt.Sprintf("- %s (%s)", p.Name, p.Model)
		if p.Name == defaultName {
			line += " [default]"
		}
		lines = append(lines, line)
	}
	return s.reply(ctx, b, strings.Join(lines, "\n"))
}

func (s *Service) aiPresetAdd(b *gotgbot.Bot, ctx *ext.Context) error {
	chatID, userID, ok := s.requireAdmin(b, ctx)
	if !ok {
		return nil
	}
	msg := ctx.EffectiveMessage
	if msg == nil {
		return nil
	}
	rem := strings.TrimSpace(commandRemainder(msg.GetText()))
	name, rem := splitFirstWord(rem)
	providerName, rem := splitFirstWord(rem)
	model, systemPrompt := splitFirstWord(rem)
	systemPrompt = strings.TrimSpace(systemPrompt)
	if name == "" || providerName == "" || model == "" || systemPrompt == "" {
		return s.reply(ctx, b, "Usage: /ai_preset_add <name> <provider> <model> <system_prompt...>")
	}

	provider, err := s.store.GetProviderByName(context.Background(), chatID, providerName)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return s.reply(ctx, b, "Provider not found.")
		}
		s.logger.Error().Err(err).Msg("get provider failed")
		return s.reply(ctx, b, "Failed to read provider.")
	}

	paramsJSON := `{"max_tokens":1024,"temperature":0.7,"allow_tools":false}`
	if err := s.store.UpsertPreset(context.Background(), storage.Preset{
		ChatID:             chatID,
		Name:               name,
		ProviderInstanceID: provider.ID,
		Model:              model,
		SystemPrompt:       systemPrompt,
		ParamsJSON:         paramsJSON,
	}); err != nil {
		s.logger.Error().Err(err).Msg("upsert preset failed")
		return s.reply(ctx, b, "Failed to save preset.")
	}

	if _, err := s.store.GetDefaultPresetName(context.Background(), chatID); errors.Is(err, storage.ErrNotFound) {
		_ = s.store.SetDefaultPreset(context.Background(), chatID, name)
	}

	_ = s.audit(chatID, userID, "preset_add", map[string]any{"name": name, "provider": providerName, "model": model})
	return s.reply(ctx, b, "Preset saved.")
}

func (s *Service) aiPresetDel(b *gotgbot.Bot, ctx *ext.Context) error {
	chatID, userID, ok := s.requireAdmin(b, ctx)
	if !ok {
		return nil
	}
	name := strings.TrimSpace(commandRemainder(ctx.EffectiveMessage.GetText()))
	if name == "" {
		return s.reply(ctx, b, "Usage: /ai_preset_del <name>")
	}
	if err := s.store.DeletePreset(context.Background(), chatID, name); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return s.reply(ctx, b, "Preset not found.")
		}
		s.logger.Error().Err(err).Msg("delete preset failed")
		return s.reply(ctx, b, "Failed to delete preset.")
	}
	if def, err := s.store.GetDefaultPresetName(context.Background(), chatID); err == nil && def == name {
		_ = s.store.ClearDefaultPreset(context.Background(), chatID)
	}
	_ = s.audit(chatID, userID, "preset_del", map[string]any{"name": name})
	return s.reply(ctx, b, "Preset deleted.")
}

func (s *Service) aiDefault(b *gotgbot.Bot, ctx *ext.Context) error {
	chatID, userID, ok := s.requireAdmin(b, ctx)
	if !ok {
		return nil
	}
	name := strings.TrimSpace(commandRemainder(ctx.EffectiveMessage.GetText()))
	if name == "" {
		return s.reply(ctx, b, "Usage: /ai_default <name>")
	}
	if _, err := s.store.GetPresetWithProviderByName(context.Background(), chatID, name); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return s.reply(ctx, b, "Preset not found.")
		}
		return s.reply(ctx, b, "Failed to read preset.")
	}
	if err := s.store.SetDefaultPreset(context.Background(), chatID, name); err != nil {
		return s.reply(ctx, b, "Failed to set default preset.")
	}
	_ = s.audit(chatID, userID, "preset_default", map[string]any{"name": name})
	return s.reply(ctx, b, "Default preset updated.")
}

func (s *Service) llmAdd(b *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.EffectiveChat == nil || ctx.EffectiveUser == nil {
		return nil
	}
	if ctx.EffectiveChat.Type == "private" {
		return s.reply(ctx, b, "Run /llm_add in your group/supergroup first.")
	}

	chatID, _, ok := s.requireAdmin(b, ctx)
	if !ok {
		return nil
	}
	s.ensureChat(context.Background(), ctx.EffectiveMessage)
	link := s.deepLink(b, fmt.Sprintf("llmadd_%d", chatID))
	if link == "" {
		return s.reply(ctx, b, "Unable to generate deep-link. Check bot username.")
	}

	return s.reply(ctx, b, "Continue in private chat: "+link)
}

func (s *Service) llmList(b *gotgbot.Bot, ctx *ext.Context) error {
	chatID, _, ok := s.requireAdmin(b, ctx)
	if !ok {
		return nil
	}
	items, err := s.store.ListProviders(context.Background(), chatID)
	if err != nil {
		return s.reply(ctx, b, "Failed to list providers.")
	}
	if len(items) == 0 {
		return s.reply(ctx, b, "No providers configured.")
	}
	lines := []string{"Providers:"}
	for _, p := range items {
		lines = append(lines, fmt.Sprintf("- %s [%s] %s", p.Name, p.Kind, p.BaseURL))
	}
	return s.reply(ctx, b, strings.Join(lines, "\n"))
}

func (s *Service) llmDel(b *gotgbot.Bot, ctx *ext.Context) error {
	chatID, userID, ok := s.requireAdmin(b, ctx)
	if !ok {
		return nil
	}
	name := strings.TrimSpace(commandRemainder(ctx.EffectiveMessage.GetText()))
	if name == "" {
		return s.reply(ctx, b, "Usage: /llm_del <name>")
	}
	if err := s.store.DeleteProviderByName(context.Background(), chatID, name); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return s.reply(ctx, b, "Provider not found.")
		}
		return s.reply(ctx, b, "Failed to delete provider.")
	}
	_ = s.audit(chatID, userID, "provider_del", map[string]any{"name": name})
	return s.reply(ctx, b, "Provider deleted.")
}

func (s *Service) privateText(b *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.EffectiveChat == nil || ctx.EffectiveUser == nil || ctx.EffectiveMessage == nil {
		return nil
	}
	if ctx.EffectiveChat.Type != "private" {
		return nil
	}
	text := strings.TrimSpace(ctx.EffectiveMessage.GetText())
	if text == "" || strings.HasPrefix(text, "/") {
		return nil
	}

	state, err := s.wizard.Get(context.Background(), ctx.EffectiveUser.Id)
	if err != nil {
		s.logger.Error().Err(err).Msg("wizard load failed")
		return s.reply(ctx, b, "Wizard state error. Start again with /llm_add.")
	}
	if state == nil {
		return nil
	}

	switch state.Step {
	case "kind":
		kind := normalizeProviderKind(text)
		if kind == "" {
			return s.reply(ctx, b, "Send provider type: openai-compat or custom-http")
		}
		state.Kind = kind
		state.Step = "name"
		if err := s.wizard.Set(context.Background(), ctx.EffectiveUser.Id, *state); err != nil {
			return s.reply(ctx, b, "Failed to persist wizard state.")
		}
		return s.reply(ctx, b, "Send provider name (letters, digits, _ or -, max 64).")

	case "name":
		if !providerNameRegex.MatchString(text) {
			return s.reply(ctx, b, "Invalid provider name. Use letters, digits, _ or -.")
		}
		state.Name = text
		state.Step = "base_url"
		if err := s.wizard.Set(context.Background(), ctx.EffectiveUser.Id, *state); err != nil {
			return s.reply(ctx, b, "Failed to persist wizard state.")
		}
		if state.Kind == "openai_compat" {
			return s.reply(ctx, b, "Send base URL (example: https://api.x.ai/v1)")
		}
		return s.reply(ctx, b, "Send custom endpoint URL")

	case "base_url":
		state.BaseURL = text
		if state.Kind == "openai_compat" {
			state.Step = "endpoint"
			if err := s.wizard.Set(context.Background(), ctx.EffectiveUser.Id, *state); err != nil {
				return s.reply(ctx, b, "Failed to persist wizard state.")
			}
			return s.reply(ctx, b, "Send endpoint mode: chat_completions or responses")
		}
		state.Step = "headers"
		if err := s.wizard.Set(context.Background(), ctx.EffectiveUser.Id, *state); err != nil {
			return s.reply(ctx, b, "Failed to persist wizard state.")
		}
		return s.reply(ctx, b, `Send headers JSON template (example: {"Authorization":"Bearer {{api_key}}"}) or '-'`)

	case "endpoint":
		mode := strings.ToLower(strings.TrimSpace(text))
		if mode != "chat_completions" && mode != "responses" {
			return s.reply(ctx, b, "Supported endpoint modes: chat_completions or responses")
		}
		state.Endpoint = mode
		state.Step = "api_key"
		if err := s.wizard.Set(context.Background(), ctx.EffectiveUser.Id, *state); err != nil {
			return s.reply(ctx, b, "Failed to persist wizard state.")
		}
		return s.reply(ctx, b, "Send API key (or '-' for empty).")

	case "headers":
		if text == "-" {
			state.HeadersJSON = ""
		} else {
			headers := map[string]string{}
			if err := json.Unmarshal([]byte(text), &headers); err != nil {
				return s.reply(ctx, b, "Invalid JSON. Example: {\"Authorization\":\"Bearer {{api_key}}\"}")
			}
			state.HeadersJSON = text
		}
		state.Step = "api_key"
		if err := s.wizard.Set(context.Background(), ctx.EffectiveUser.Id, *state); err != nil {
			return s.reply(ctx, b, "Failed to persist wizard state.")
		}
		return s.reply(ctx, b, "Send API key (or '-' for empty).")

	case "api_key":
		apiKey := text
		if apiKey == "-" {
			apiKey = ""
		}
		if err := s.finishWizard(ctx.EffectiveUser.Id, state, apiKey); err != nil {
			s.logger.Error().Err(err).Msg("finish wizard failed")
			return s.reply(ctx, b, "Failed to save provider. Try again with /llm_add.")
		}
		_ = s.wizard.Clear(context.Background(), ctx.EffectiveUser.Id)
		return s.reply(ctx, b, "Provider saved. Use /llm_list in group.")
	}

	return nil
}

func (s *Service) beginLLMAddWizard(ctx *ext.Context, b *gotgbot.Bot, targetChatID int64) error {
	if ctx.EffectiveUser == nil || ctx.EffectiveChat == nil || ctx.EffectiveChat.Type != "private" {
		return nil
	}
	admin, err := s.isAdmin(context.Background(), b, targetChatID, ctx.EffectiveUser.Id)
	if err != nil {
		s.logger.Error().Err(err).Int64("chat_id", targetChatID).Msg("admin check failed in dm wizard")
		return s.reply(ctx, b, "Could not verify admin rights. Please retry.")
	}
	if !admin {
		return s.reply(ctx, b, "You are not an admin in that chat.")
	}
	_ = s.store.EnsureChat(context.Background(), targetChatID, "group", "")
	state := llmWizardState{TargetChatID: targetChatID, Step: "kind"}
	if err := s.wizard.Set(context.Background(), ctx.EffectiveUser.Id, state); err != nil {
		return s.reply(ctx, b, "Failed to start wizard.")
	}
	return s.reply(ctx, b, "Wizard started. Send provider type: openai-compat or custom-http")
}

func (s *Service) finishWizard(actorUserID int64, state *llmWizardState, apiKey string) error {
	var encAPIKey *string
	if strings.TrimSpace(apiKey) != "" {
		v, err := s.crypto.MarshalEncryptedString(apiKey)
		if err != nil {
			return err
		}
		encAPIKey = &v
	}

	var encHeaders *string
	if strings.TrimSpace(state.HeadersJSON) != "" {
		v, err := s.crypto.MarshalEncryptedString(state.HeadersJSON)
		if err != nil {
			return err
		}
		encHeaders = &v
	}

	cfg := map[string]any{}
	if state.Kind == "openai_compat" {
		cfg["endpoint"] = state.Endpoint
	}
	cfgJSON, _ := json.Marshal(cfg)

	_, err := s.store.UpsertProviderInstance(context.Background(), storage.ProviderInstance{
		ChatID:         state.TargetChatID,
		Name:           state.Name,
		Kind:           state.Kind,
		BaseURL:        state.BaseURL,
		EncAPIKey:      encAPIKey,
		EncHeadersJSON: encHeaders,
		ConfigJSON:     string(cfgJSON),
	})
	if err != nil {
		return err
	}
	_ = s.audit(state.TargetChatID, actorUserID, "provider_add", map[string]any{"name": state.Name, "kind": state.Kind})
	return nil
}

func (s *Service) requireAdmin(b *gotgbot.Bot, ctx *ext.Context) (chatID int64, uid int64, ok bool) {
	if ctx.EffectiveChat == nil || ctx.EffectiveUser == nil {
		return 0, 0, false
	}
	if ctx.EffectiveChat.Type == "private" {
		_ = s.reply(ctx, b, "Run this command in group/supergroup.")
		return 0, 0, false
	}
	chatID = ctx.EffectiveChat.Id
	uid = ctx.EffectiveUser.Id
	admin, err := s.isAdmin(context.Background(), b, chatID, uid)
	if err != nil {
		s.logger.Error().Err(err).Int64("chat_id", chatID).Int64("user_id", uid).Msg("admin check failed")
		_ = s.reply(ctx, b, "Failed to verify admin rights.")
		return 0, 0, false
	}
	if !admin {
		_ = s.reply(ctx, b, "Only chat admins can run this command.")
		return 0, 0, false
	}
	if ctx.EffectiveMessage != nil {
		s.ensureChat(context.Background(), ctx.EffectiveMessage)
	}
	return chatID, uid, true
}

func (s *Service) isAdmin(ctx context.Context, b *gotgbot.Bot, chatID, userID int64) (bool, error) {
	cacheKey := fmt.Sprintf("hyprbot:admin:%d:%d", chatID, userID)
	if v, err := s.redis.Get(ctx, cacheKey).Result(); err == nil {
		return v == "1", nil
	} else if err != redis.Nil {
		s.logger.Warn().Err(err).Msg("failed to read admin cache")
	}

	member, err := b.GetChatMemberWithContext(ctx, chatID, userID, nil)
	if err != nil {
		return false, err
	}
	status := member.GetStatus()
	admin := status == "administrator" || status == "creator"

	value := "0"
	if admin {
		value = "1"
	}
	_ = s.redis.Set(ctx, cacheKey, value, s.adminCacheTTL).Err()
	_ = s.store.SetAdminCache(ctx, chatID, userID, admin)
	return admin, nil
}

func (s *Service) allowRate(chatID, userID int64, b *gotgbot.Bot, ctx *ext.Context) bool {
	if userID == 0 || s.rateLimiter == nil {
		return true
	}
	ok, _, resetAt, err := s.rateLimiter.Allow(context.Background(), chatID, userID, s.now())
	if err != nil {
		s.logger.Error().Err(err).Msg("rate limiter failed")
		return true
	}
	if ok {
		return true
	}
	_ = s.reply(ctx, b, "Rate limit exceeded. Try again after "+resetAt.Format("15:04 UTC"))
	return false
}

func (s *Service) audit(chatID, userID int64, action string, meta map[string]any) error {
	b, _ := json.Marshal(meta)
	return s.store.LogAction(context.Background(), storage.AuditEntry{
		ChatID:   chatID,
		UserID:   userID,
		Action:   action,
		MetaJSON: string(b),
	})
}

func (s *Service) reply(ctx *ext.Context, b *gotgbot.Bot, text string) error {
	if ctx.EffectiveChat == nil {
		return nil
	}
	_, err := b.SendMessage(ctx.EffectiveChat.Id, text, nil)
	return err
}

func commandRemainder(text string) string {
	parts := strings.SplitN(strings.TrimSpace(text), " ", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func splitFirstWord(s string) (first string, rest string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	idx := strings.IndexByte(s, ' ')
	if idx < 0 {
		return s, ""
	}
	return s[:idx], strings.TrimSpace(s[idx+1:])
}

func normalizeProviderKind(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "openai", "openai_compat", "openai-compatible", "openai-compat":
		return "openai_compat"
	case "custom_http", "custom-http", "custom":
		return "custom_http"
	default:
		return ""
	}
}

func userID(ctx *ext.Context) int64 {
	if ctx.EffectiveUser == nil {
		return 0
	}
	return ctx.EffectiveUser.Id
}
