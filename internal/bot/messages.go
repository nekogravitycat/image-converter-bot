package bot

import (
	"context"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// candidateExtensions only pre-filter which attachments are worth downloading; the real
// format is decided by sniffing the bytes in imageproc.
var candidateExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".jfif": true, ".png": true, ".webp": true,
	".heic": true, ".heif": true, ".avif": true, ".gif": true, ".tif": true, ".tiff": true,
}

func isImageCandidate(att discord.Attachment) bool {
	if att.ContentType != nil && strings.HasPrefix(strings.ToLower(*att.ContentType), "image/") {
		return true
	}
	return candidateExtensions[strings.ToLower(path.Ext(att.Filename))]
}

// OnGuildMessageCreate is the gateway listener for guild messages.
func (b *Bot) OnGuildMessageCreate(e *events.GuildMessageCreate) {
	b.handleMessage(context.Background(), e.GuildID, e.Message)
}

// handleMessage queues one conversion job per image attachment in allowlisted channels.
func (b *Bot) handleMessage(ctx context.Context, guildID snowflake.ID, msg discord.Message) {
	// Never react to bots (including ourselves) or webhooks: prevents conversion loops.
	if msg.Author.Bot || msg.Author.System || msg.WebhookID != nil || len(msg.Attachments) == 0 {
		return
	}
	log := b.logger.With(
		slog.String("guild_id", guildID.String()),
		slog.String("channel_id", msg.ChannelID.String()),
		slog.String("message_id", msg.ID.String()),
	)

	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	cfg, err := b.configs.Get(cctx, guildID)
	cancel()
	if err != nil {
		log.Error("load guild config", slog.Any("err", err))
		return
	}
	if !cfg.HasChannel(msg.ChannelID) {
		return
	}

	queueFull := false
	for _, att := range msg.Attachments {
		if !isImageCandidate(att) {
			continue
		}
		job := func(ctx context.Context) { b.runAutoJob(ctx, guildID, msg.ChannelID, msg.ID, att, log) }
		if !b.queue.TrySubmit(job) {
			log.Warn("work queue full, dropping attachment", slog.String("attachment_id", att.ID.String()))
			queueFull = true
		}
	}
	if queueFull {
		// Reply off the gateway goroutine so a slow REST call cannot stall event dispatch.
		go b.reply(context.Background(), msg.ChannelID, msg.ID, msgQueueFull, log)
	}
}

func (b *Bot) runAutoJob(ctx context.Context, guildID, channelID, sourceID snowflake.ID, att discord.Attachment, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, b.settings.JobTimeout)
	defer cancel()
	start := time.Now()
	log = log.With(slog.String("attachment_id", att.ID.String()))

	// Re-read config at run time so changes made while queued still apply.
	cfg, err := b.configs.Get(ctx, guildID)
	if err != nil {
		log.Error("load guild config", slog.Any("err", err))
		return
	}
	res, inputBytes, err := b.convertAttachment(ctx, att, toOptions(cfg))
	if err != nil {
		log.Error("conversion failed", slog.Any("err", err), slog.Int64("duration_ms", time.Since(start).Milliseconds()))
		b.reply(ctx, channelID, sourceID, "`"+displayName(att.Filename)+"`: "+userMessage(err), log)
		return
	}

	summary := resultSummary(res)
	actx, acancel := b.apiCtx(ctx)
	msg, err := b.api.CreateMessage(channelID, discord.MessageCreate{
		Content:          summary,
		Files:            []*discord.File{outputFile(res)},
		MessageReference: &discord.MessageReference{MessageID: &sourceID, FailIfNotExists: false},
		AllowedMentions:  noMentions,
	}, rest.WithCtx(actx))
	acancel()
	if err != nil {
		err = classifyUploadError(err)
		log.Error("upload converted image", slog.Any("err", err), slog.Int("output_bytes", len(res.Data)))
		b.reply(ctx, channelID, sourceID, "`"+displayName(att.Filename)+"`: "+userMessage(err), log)
		return
	}

	b.finalizeResult(ctx, msg, summary, func(ctx context.Context, u discord.MessageUpdate) error {
		_, err := b.api.UpdateMessage(channelID, msg.ID, u, rest.WithCtx(ctx))
		return err
	}, log)
	logConverted(log, res, inputBytes, start)
}

// reply posts a plain text reply to the source message, logging (not returning) failures.
func (b *Bot) reply(ctx context.Context, channelID, sourceID snowflake.ID, text string, log *slog.Logger) {
	ctx, cancel := b.apiCtx(ctx)
	defer cancel()
	if _, err := b.api.CreateMessage(channelID, discord.MessageCreate{
		Content:          text,
		MessageReference: &discord.MessageReference{MessageID: &sourceID, FailIfNotExists: false},
		AllowedMentions:  noMentions,
	}, rest.WithCtx(ctx)); err != nil {
		log.Error("send reply", slog.Any("err", err))
	}
}
