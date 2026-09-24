package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/nekogravitycat/image-converter-bot/internal/discordurl"
)

// OnApplicationCommand dispatches slash commands.
func (b *Bot) OnApplicationCommand(e *events.ApplicationCommandInteractionCreate) {
	data, ok := e.Data.(discord.SlashCommandInteractionData)
	if !ok {
		return
	}
	guildID := e.GuildID()
	if guildID == nil {
		_ = e.CreateMessage(ephemeral("This command can only be used in a server."))
		return
	}

	switch data.CommandName() {
	case "convert":
		b.onConvert(e, *guildID, data)
	case "config":
		var perms discord.Permissions
		if m := e.Member(); m != nil {
			perms = m.Permissions
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second) // interactions must be answered within 3s
		defer cancel()
		if err := e.CreateMessage(b.handleConfig(ctx, *guildID, perms, data)); err != nil {
			b.logger.Error("respond to /config", slog.Any("err", err))
		}
	}
}

// OnComponent dispatches button presses.
func (b *Bot) OnComponent(e *events.ComponentInteractionCreate) {
	switch e.Data.CustomID() {
	case discordurl.RefreshButtonID:
		b.onRefresh(e)
	case resetConfirmID, resetCancelID:
		guildID := e.GuildID()
		if guildID == nil {
			return
		}
		var perms discord.Permissions
		if m := e.Member(); m != nil {
			perms = m.Permissions
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := e.UpdateMessage(b.handleResetButton(ctx, *guildID, perms, e.Data.CustomID() == resetConfirmID)); err != nil {
			b.logger.Error("respond to reset button", slog.Any("err", err))
		}
	}
}

func (b *Bot) onConvert(e *events.ApplicationCommandInteractionCreate, guildID snowflake.ID, data discord.SlashCommandInteractionData) {
	att, ok := data.OptAttachment("image")
	if !ok {
		_ = e.CreateMessage(ephemeral("Please attach an image."))
		return
	}
	log := b.logger.With(
		slog.String("guild_id", guildID.String()),
		slog.String("channel_id", e.Channel().ID().String()),
		slog.String("interaction_id", e.ID().String()),
		slog.String("attachment_id", att.ID.String()),
	)
	// Acknowledge first: conversion can outlast Discord's 3-second response window.
	if err := e.DeferCreateMessage(false); err != nil {
		log.Error("defer /convert response", slog.Any("err", err))
		return
	}
	appID, token := e.ApplicationID(), e.Token()
	job := func(ctx context.Context) { b.runConvertJob(ctx, guildID, appID, token, att, log) }
	if !b.queue.TrySubmit(job) {
		log.Warn("work queue full, rejecting /convert")
		b.editResponseText(context.Background(), appID, token, msgQueueFull, log)
	}
}

func (b *Bot) runConvertJob(ctx context.Context, guildID, appID snowflake.ID, token string, att discord.Attachment, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, b.settings.JobTimeout)
	defer cancel()
	start := time.Now()

	cfg, err := b.configs.Get(ctx, guildID)
	if err != nil {
		log.Error("load guild config", slog.Any("err", err))
		b.editResponseText(ctx, appID, token, userMessage(err), log)
		return
	}
	res, inputBytes, err := b.convertAttachment(ctx, att, toOptions(cfg))
	if err != nil {
		log.Error("conversion failed", slog.Any("err", err), slog.Int64("duration_ms", time.Since(start).Milliseconds()))
		b.editResponseText(ctx, appID, token, userMessage(err), log)
		return
	}

	summary := resultSummary(res)
	actx, acancel := b.apiCtx(ctx)
	msg, err := b.api.UpdateInteractionResponse(appID, token, discord.MessageUpdate{
		Content:         &summary,
		Files:           []*discord.File{outputFile(res)},
		AllowedMentions: noMentions,
	}, rest.WithCtx(actx))
	acancel()
	if err != nil {
		err = classifyUploadError(err)
		log.Error("upload converted image", slog.Any("err", err), slog.Int("output_bytes", len(res.Data)))
		b.editResponseText(ctx, appID, token, userMessage(err), log)
		return
	}

	b.finalizeResult(ctx, msg, summary, func(ctx context.Context, u discord.MessageUpdate) error {
		_, err := b.api.UpdateInteractionResponse(appID, token, u, rest.WithCtx(ctx))
		return err
	}, log)
	logConverted(log, res, inputBytes, start)
}

func (b *Bot) editResponseText(ctx context.Context, appID snowflake.ID, token, text string, log *slog.Logger) {
	ctx, cancel := b.apiCtx(ctx)
	defer cancel()
	if _, err := b.api.UpdateInteractionResponse(appID, token, discord.MessageUpdate{Content: &text}, rest.WithCtx(ctx)); err != nil {
		log.Error("edit interaction response", slog.Any("err", err))
	}
}

func (b *Bot) onRefresh(e *events.ComponentInteractionCreate) {
	log := b.logger.With(slog.String("channel_id", e.Channel().ID().String()), slog.String("result_message_id", e.Message.ID.String()))
	if gid := e.GuildID(); gid != nil {
		log = log.With(slog.String("guild_id", gid.String()))
	}
	if err := e.DeferUpdateMessage(); err != nil {
		log.Error("defer refresh", slog.Any("err", err))
		return
	}
	appID, token := e.ApplicationID(), e.Token()
	ref := refreshTarget{appID: appID, token: token, channelID: e.Channel().ID(), messageID: e.Message.ID}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := b.refreshURL(ctx, ref); err != nil {
			log.Warn("refresh URL failed", slog.Any("err", err))
			fctx, fcancel := b.apiCtx(ctx)
			defer fcancel()
			if _, ferr := b.api.CreateFollowupMessage(appID, token, ephemeral(refreshErrorMessage(err)), rest.WithCtx(fctx)); ferr != nil {
				log.Error("send refresh error", slog.Any("err", ferr))
			}
			return
		}
		log.Info("attachment URL refreshed")
	}()
}

type refreshTarget struct {
	appID     snowflake.ID
	token     string
	channelID snowflake.ID
	messageID snowflake.ID
}

var (
	errRefreshFetch        = errors.New("fetch result message")
	errRefreshNoAttachment = errors.New("result message has no attachment")
	errRefreshExpiry       = errors.New("attachment URL has no parsable expiry")
)

// refreshURL re-fetches the result message (Discord signs attachment URLs on every read)
// and edits the same message with the new URL. Nothing is re-encoded or re-uploaded, and
// no state is needed beyond the interaction itself.
func (b *Bot) refreshURL(ctx context.Context, t refreshTarget) error {
	fctx, cancel := b.apiCtx(ctx)
	msg, err := b.api.GetInteractionResponse(t.appID, t.token, rest.WithCtx(fctx))
	cancel()
	if err != nil {
		// Fall back to the channel endpoint in case the webhook lookup is unavailable.
		fctx, cancel := b.apiCtx(ctx)
		msg, err = b.api.GetMessage(t.channelID, t.messageID, rest.WithCtx(fctx))
		cancel()
		if err != nil {
			return fmt.Errorf("%w: %w", errRefreshFetch, err)
		}
	}
	if len(msg.Attachments) == 0 {
		return errRefreshNoAttachment
	}
	url := msg.Attachments[0].URL
	expiresAt, err := discordurl.ParseExpiry(url)
	if err != nil {
		return fmt.Errorf("%w: %w", errRefreshExpiry, err)
	}

	content := discordurl.ResultContent(discordurl.SummaryFromContent(msg.Content), url, expiresAt)
	components := discordurl.Components()
	uctx, cancel := b.apiCtx(ctx)
	defer cancel()
	if _, err := b.api.UpdateInteractionResponse(t.appID, t.token,
		discord.MessageUpdate{Content: &content, Components: &components}, rest.WithCtx(uctx)); err != nil {
		return fmt.Errorf("edit result message: %w", err)
	}
	return nil
}

func refreshErrorMessage(err error) string {
	switch {
	case errors.Is(err, errRefreshFetch):
		return "Could not refresh the URL: the message could not be loaded. It may have been deleted."
	case errors.Is(err, errRefreshNoAttachment):
		return "Could not refresh the URL: this message no longer has an image attached."
	case errors.Is(err, errRefreshExpiry):
		return "Could not refresh the URL: Discord returned an unexpected attachment URL."
	default:
		return "Could not refresh the URL. Please try again later."
	}
}
