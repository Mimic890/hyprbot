package telegram

import (
	"fmt"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
)

func (s *Service) onCallback(b *gotgbot.Bot, ctx *ext.Context) error {
	if ctx == nil || ctx.CallbackQuery == nil {
		return nil
	}

	data := strings.TrimSpace(ctx.CallbackQuery.Data)
	s.answerCallback(b, ctx, "", false)

	switch data {
	case cbMenu:
		return s.editOrReplyCallback(ctx, b, s.mainMenuText(ctx), s.mainMenuKeyboard())

	case cbHowAsk:
		return s.editOrReplyCallback(ctx, b, s.askUsageText(), s.backToMenuKeyboard())

	case cbHowAI:
		return s.editOrReplyCallback(ctx, b, s.aiUsageText(), s.backToMenuKeyboard())

	case cbSetup:
		return s.editOrReplyCallback(ctx, b, s.setupText(), s.setupKeyboard())

	case cbStatus:
		return s.editOrReplyCallback(ctx, b, s.statusText(ctx), s.backToMenuKeyboard())

	case cbListPresets:
		chatID, ok := s.callbackChatID(ctx)
		if !ok {
			s.answerCallback(b, ctx, "Chat is unavailable for this action.", true)
			return nil
		}
		text, err := s.buildPresetListText(chatID)
		if err != nil {
			s.answerCallback(b, ctx, "Failed to load presets.", true)
			return nil
		}
		return s.editOrReplyCallback(ctx, b, text, s.backToMenuKeyboard())

	case cbListProviders:
		chatID, _, ok := s.requireAdmin(b, ctx)
		if !ok {
			s.answerCallback(b, ctx, "Only chat admins can view providers.", true)
			return nil
		}
		text, err := s.buildProviderListText(chatID)
		if err != nil {
			s.answerCallback(b, ctx, "Failed to load providers.", true)
			return nil
		}
		return s.editOrReplyCallback(ctx, b, text, s.backToMenuKeyboard())

	case cbAdminHelp:
		return s.editOrReplyCallback(ctx, b, s.adminHelpText(), s.backToMenuKeyboard())

	case cbActLlmAdd:
		if _, _, ok := s.requireAdmin(b, ctx); !ok {
			s.answerCallback(b, ctx, "Only chat admins can add providers.", true)
			return nil
		}
		if err := s.llmAdd(b, ctx); err != nil {
			return err
		}
		s.answerCallback(b, ctx, "Deep-link sent to chat.", false)
		return nil

	case cbActLlmList:
		if _, _, ok := s.requireAdmin(b, ctx); !ok {
			s.answerCallback(b, ctx, "Only chat admins can list providers.", true)
			return nil
		}
		if err := s.llmList(b, ctx); err != nil {
			return err
		}
		s.answerCallback(b, ctx, "Provider summary sent.", false)
		return nil

	default:
		s.answerCallback(b, ctx, fmt.Sprintf("Unknown action: %s", data), true)
		return nil
	}
}

func (s *Service) answerCallback(b *gotgbot.Bot, ctx *ext.Context, text string, alert bool) {
	if ctx == nil || ctx.CallbackQuery == nil {
		return
	}
	opts := &gotgbot.AnswerCallbackQueryOpts{ShowAlert: alert}
	if text != "" {
		opts.Text = text
	}
	_, _ = b.AnswerCallbackQuery(ctx.CallbackQuery.Id, opts)
}

func (s *Service) editOrReplyCallback(ctx *ext.Context, b *gotgbot.Bot, text string, markup *gotgbot.InlineKeyboardMarkup) error {
	if ctx != nil && ctx.CallbackQuery != nil && ctx.CallbackQuery.Message != nil {
		opts := &gotgbot.EditMessageTextOpts{}
		if markup != nil {
			opts.ReplyMarkup = *markup
		}
		_, _, err := ctx.CallbackQuery.Message.EditText(b, text, opts)
		if err == nil {
			return nil
		}
		if strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
			return nil
		}
		// Fallback to sending a regular message if edit failed.
	}
	return s.replyWithMarkup(ctx, b, text, markup)
}

func (s *Service) callbackChatID(ctx *ext.Context) (int64, bool) {
	if ctx != nil && ctx.EffectiveChat != nil {
		return ctx.EffectiveChat.Id, true
	}
	if ctx != nil && ctx.CallbackQuery != nil && ctx.CallbackQuery.Message != nil {
		chat := ctx.CallbackQuery.Message.GetChat()
		return chat.Id, true
	}
	return 0, false
}
