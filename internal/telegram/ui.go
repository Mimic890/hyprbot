package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"hyprbot/internal/storage"
)

const (
	cbPrefix = "hb:"

	cbMenu          = cbPrefix + "menu"
	cbHowAsk        = cbPrefix + "how_ask"
	cbHowAI         = cbPrefix + "how_ai"
	cbSetup         = cbPrefix + "setup"
	cbStatus        = cbPrefix + "status"
	cbListPresets   = cbPrefix + "list_presets"
	cbListProviders = cbPrefix + "list_providers"
	cbAdminHelp     = cbPrefix + "admin_help"
	cbActLlmAdd     = cbPrefix + "act_llm_add"
	cbActLlmList    = cbPrefix + "act_llm_list"
)

func (s *Service) menu(b *gotgbot.Bot, ctx *ext.Context) error {
	return s.sendMainMenu(ctx, b)
}

func (s *Service) setup(b *gotgbot.Bot, ctx *ext.Context) error {
	return s.replyWithMarkup(ctx, b, s.setupText(), s.setupKeyboard())
}

func (s *Service) status(b *gotgbot.Bot, ctx *ext.Context) error {
	text := s.statusText(ctx)
	return s.replyWithMarkup(ctx, b, text, s.backToMenuKeyboard())
}

func (s *Service) sendMainMenu(ctx *ext.Context, b *gotgbot.Bot) error {
	return s.replyWithMarkup(ctx, b, s.mainMenuText(ctx), s.mainMenuKeyboard())
}

func (s *Service) mainMenuText(ctx *ext.Context) string {
	chatType := "unknown"
	if ctx != nil && ctx.EffectiveChat != nil {
		chatType = ctx.EffectiveChat.Type
	}

	lines := []string{
		"HyprBot menu",
		"",
		"Quick commands:",
		"/ask <text> - ask using default preset",
		"/ai <preset> <text> - ask using explicit preset",
		"/ai_list - list chat presets",
		"/status - chat status",
		"",
		"Admin commands (group/supergroup):",
		"/llm_add, /llm_list, /llm_del",
		"/ai_preset_add, /ai_preset_del, /ai_default",
		"",
		fmt.Sprintf("Chat type: %s", chatType),
		fmt.Sprintf("Access mode: %s", s.accessMode),
		"Use the inline buttons below for navigation.",
	}
	return strings.Join(lines, "\n")
}

func (s *Service) setupText() string {
	return strings.Join([]string{
		"Setup flow for a new group:",
		"1) In the group run /llm_add",
		"2) Open the private deep-link from the bot message",
		"3) Finish provider wizard in private chat",
		"4) Back in group, create preset:",
		"   /ai_preset_add <name> <provider> <model> <system_prompt...>",
		"5) Set default preset: /ai_default <name>",
		"6) Ask: /ask <text>",
	}, "\n")
}

func (s *Service) askUsageText() string {
	return strings.Join([]string{
		"How to use /ask",
		"",
		"Syntax:",
		"/ask <text>",
		"",
		"Behavior:",
		"- Uses the chat default preset",
		"- Queues request asynchronously",
		"- Sends reply when worker finishes",
	}, "\n")
}

func (s *Service) aiUsageText() string {
	return strings.Join([]string{
		"How to use /ai",
		"",
		"Syntax:",
		"/ai <preset> <text>",
		"",
		"Behavior:",
		"- Uses explicit preset",
		"- Good when chat has several presets",
		"- Use /ai_list to see available names",
	}, "\n")
}

func (s *Service) adminHelpText() string {
	return strings.Join([]string{
		"Admin quick reference",
		"",
		"Providers:",
		"/llm_add",
		"/llm_list",
		"/llm_del <name>",
		"",
		"Presets:",
		"/ai_preset_add <name> <provider> <model> <system_prompt...>",
		"/ai_preset_del <name>",
		"/ai_default <name>",
	}, "\n")
}

func (s *Service) statusText(ctx *ext.Context) string {
	if ctx == nil || ctx.EffectiveChat == nil {
		return "Chat is not available for status."
	}

	chatID := ctx.EffectiveChat.Id
	chatType := ctx.EffectiveChat.Type

	presetCount := 0
	if presets, err := s.store.ListPresets(context.Background(), chatID); err == nil {
		presetCount = len(presets)
	}

	providerCount := 0
	if providers, err := s.store.ListProviders(context.Background(), chatID); err == nil {
		providerCount = len(providers)
	}

	defaultPreset := "<not set>"
	if name, err := s.store.GetDefaultPresetName(context.Background(), chatID); err == nil {
		defaultPreset = name
	}

	return strings.Join([]string{
		"Chat status",
		fmt.Sprintf("chat_id: %d", chatID),
		fmt.Sprintf("chat_type: %s", chatType),
		fmt.Sprintf("providers: %d", providerCount),
		fmt.Sprintf("presets: %d", presetCount),
		fmt.Sprintf("default_preset: %s", defaultPreset),
		fmt.Sprintf("access_mode: %s", s.accessMode),
	}, "\n")
}

func (s *Service) buildPresetListText(chatID int64) (string, error) {
	presets, err := s.store.ListPresets(context.Background(), chatID)
	if err != nil {
		return "", err
	}
	if len(presets) == 0 {
		return "No presets configured for this chat.", nil
	}

	defaultName, _ := s.store.GetDefaultPresetName(context.Background(), chatID)
	lines := []string{"Presets:"}
	for _, p := range presets {
		line := fmt.Sprintf("- %s (%s)", p.Name, p.Model)
		if p.Name == defaultName {
			line += " [default]"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Service) buildProviderListText(chatID int64) (string, error) {
	providers, err := s.store.ListProviders(context.Background(), chatID)
	if err != nil {
		return "", err
	}
	if len(providers) == 0 {
		return "No providers configured for this chat.", nil
	}
	lines := []string{"Providers:"}
	for _, p := range providers {
		lines = append(lines, fmt.Sprintf("- %s [%s] %s", p.Name, p.Kind, p.BaseURL))
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Service) mainMenuKeyboard() *gotgbot.InlineKeyboardMarkup {
	return &gotgbot.InlineKeyboardMarkup{InlineKeyboard: [][]gotgbot.InlineKeyboardButton{
		{
			{Text: "How /ask works", CallbackData: cbHowAsk},
			{Text: "How /ai works", CallbackData: cbHowAI},
		},
		{
			{Text: "List presets", CallbackData: cbListPresets},
			{Text: "Chat status", CallbackData: cbStatus},
		},
		{
			{Text: "List providers", CallbackData: cbListProviders},
			{Text: "Admin help", CallbackData: cbAdminHelp},
		},
		{
			{Text: "Add provider", CallbackData: cbActLlmAdd},
			{Text: "Provider summary", CallbackData: cbActLlmList},
		},
		{
			{Text: "Setup guide", CallbackData: cbSetup},
			{Text: "Refresh", CallbackData: cbMenu},
		},
	}}
}

func (s *Service) backToMenuKeyboard() *gotgbot.InlineKeyboardMarkup {
	return &gotgbot.InlineKeyboardMarkup{InlineKeyboard: [][]gotgbot.InlineKeyboardButton{
		{{Text: "Back to menu", CallbackData: cbMenu}},
	}}
}

func (s *Service) setupKeyboard() *gotgbot.InlineKeyboardMarkup {
	return &gotgbot.InlineKeyboardMarkup{InlineKeyboard: [][]gotgbot.InlineKeyboardButton{
		{
			{Text: "Add provider", CallbackData: cbActLlmAdd},
			{Text: "Back to menu", CallbackData: cbMenu},
		},
	}}
}

func (s *Service) replyWithMarkup(ctx *ext.Context, b *gotgbot.Bot, text string, markup *gotgbot.InlineKeyboardMarkup) error {
	if ctx == nil || ctx.EffectiveChat == nil {
		return nil
	}
	opts := &gotgbot.SendMessageOpts{}
	if markup != nil {
		opts.ReplyMarkup = *markup
	}
	_, err := b.SendMessage(ctx.EffectiveChat.Id, text, opts)
	return err
}

func isStorageNotFound(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}
