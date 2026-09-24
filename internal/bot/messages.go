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
		// Mark queued before TrySubmit so the reaction is visible before the job
		// can possibly start running, even if a worker picks it up immediately.
		b.markQueued(ctx, msg.ChannelID, msg.ID, log)
		job := func(ctx context.Context) { b.runAutoJob(ctx, guildID, msg.ChannelID, msg.ID, att, log) }
		if !b.queue.TrySubmit(job) {
			log.Warn("work queue full, dropping attachment", slog.String("attachment_id", att.ID.String()))
			queueFull = true
			b.markDone(ctx, msg.ChannelID, msg.ID, false, log) // roll back: this attachment never queued
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

	b.markProcessing(ctx, channelID, sourceID, log)

	succeeded := false
	var deleteOriginal bool
	// Use a fresh context for cleanup: ctx may already be expiring as the job ends.
	defer func() {
		bg := context.Background()
		if last, allOK := b.markDone(bg, channelID, sourceID, succeeded, log); last && allOK && deleteOriginal {
			b.deleteMessage(bg, channelID, sourceID, log)
		}
	}()

	// Re-read config at run time so changes made while queued still apply.
	cfg, err := b.configs.Get(ctx, guildID)
	if err != nil {
		log.Error("load guild config", slog.Any("err", err))
		return
	}
	deleteOriginal = cfg.DeleteOriginal
	res, inputBytes, err := b.convertAttachment(ctx, att, toOptions(cfg))
	if err != nil {
		log.Error("conversion failed", slog.Any("err", err), slog.Int64("duration_ms", time.Since(start).Milliseconds()))
		b.reply(ctx, channelID, sourceID, "`"+displayName(att.Filename)+"`: "+userMessage(err), log)
		return
	}

	summary := resultSummary(res)
	create := discord.MessageCreate{
		Content:         summary,
		Files:           []*discord.File{outputFile(res)},
		AllowedMentions: noMentions,
	}
	// The original message is left alone unless it will be deleted after conversion;
	// a reply to a message we're about to remove would just look broken.
	if !deleteOriginal {
		create.MessageReference = &discord.MessageReference{MessageID: &sourceID, FailIfNotExists: false}
	}
	actx, acancel := b.apiCtx(ctx)
	msg, err := b.api.CreateMessage(channelID, create, rest.WithCtx(actx))
	acancel()
	if err != nil {
		err = classifyUploadError(err)
		log.Error("upload converted image", slog.Any("err", err), slog.Int("output_bytes", len(res.Data)))
		b.reply(ctx, channelID, sourceID, "`"+displayName(att.Filename)+"`: "+userMessage(err), log)
		return
	}

	succeeded = true
	b.finalizeResult(ctx, msg, summary, func(ctx context.Context, u discord.MessageUpdate) error {
		_, err := b.api.UpdateMessage(channelID, msg.ID, u, rest.WithCtx(ctx))
		return err
	}, log)
	logConverted(log, res, inputBytes, start)
}

// deleteMessage removes messageID, logging (not returning) failures. It is used to delete a
// user's original message once every attachment on it has been converted, when configured.
func (b *Bot) deleteMessage(ctx context.Context, channelID, messageID snowflake.ID, log *slog.Logger) {
	ctx, cancel := b.apiCtx(ctx)
	defer cancel()
	if err := b.api.DeleteMessage(channelID, messageID, rest.WithCtx(ctx)); err != nil {
		if rest.IsJSONErrorCode(err, rest.JSONErrorCodeUnknownMessage) {
			return
		}
		log.Warn("delete original message", slog.Any("err", err))
	}
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
