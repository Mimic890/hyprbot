package telegram

import (
	"context"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/rs/zerolog"

	"hyprbot/internal/metrics"
	"hyprbot/internal/queue"
)

type Processor struct {
	Base    ext.BaseProcessor
	Dedupe  *queue.UpdateDeduplicator
	Metrics *metrics.Metrics
	Logger  zerolog.Logger
}

func (p Processor) ProcessUpdate(d *ext.Dispatcher, b *gotgbot.Bot, ctx *ext.Context) error {
	if p.Metrics != nil {
		p.Metrics.UpdatesTotal.Inc()
	}
	if p.Dedupe != nil {
		first, err := p.Dedupe.MarkFirst(context.Background(), ctx.UpdateId)
		if err != nil {
			p.Logger.Error().Err(err).Int64("update_id", ctx.UpdateId).Msg("failed to dedupe update")
		} else if !first {
			return nil
		}
	}
	return p.Base.ProcessUpdate(d, b, ctx)
}
