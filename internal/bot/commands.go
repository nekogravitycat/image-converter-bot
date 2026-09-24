package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"

	"github.com/nekogravitycat/image-converter-bot/internal/config"
)

const (
	resetConfirmID = "config_reset_confirm"
	resetCancelID  = "config_reset_cancel"
)

func ptr[T any](v T) *T { return &v }

// Commands returns the application commands to register. Registration uses bulk overwrite,
// so restarting never creates duplicates.
func Commands() []discord.ApplicationCommandCreate {
	guildOnly := []discord.InteractionContextType{discord.InteractionContextTypeGuild}
	manageGuild := discord.PermissionManageGuild
	enabled := func(desc string) []discord.ApplicationCommandOption {
		return []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionBool{Name: "enabled", Description: desc, Required: true},
		}
	}
	channelOpt := []discord.ApplicationCommandOption{
		discord.ApplicationCommandOptionChannel{
			Name: "channel", Description: "Text channel", Required: true,
			ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildText, discord.ChannelTypeGuildNews},
		},
	}

	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:        "convert",
			Description: "Convert an image for VRChat using this server's settings",
			Contexts:    guildOnly,
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionAttachment{Name: "image", Description: "Image to convert", Required: true},
			},
		},
		discord.SlashCommandCreate{
			Name:                     "config",
			Description:              "Configure the image converter",
			Contexts:                 guildOnly,
			DefaultMemberPermissions: omit.NewPtr(manageGuild),
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionSubCommand{Name: "show", Description: "Show the current configuration"},
				discord.ApplicationCommandOptionSubCommandGroup{
					Name: "channel", Description: "Manage channels with automatic conversion",
					Options: []discord.ApplicationCommandOptionSubCommand{
						{Name: "add", Description: "Enable automatic conversion in a channel", Options: channelOpt},
						{Name: "remove", Description: "Disable automatic conversion in a channel", Options: channelOpt},
						{Name: "list", Description: "List channels with automatic conversion"},
					},
				},
				discord.ApplicationCommandOptionSubCommand{
					Name: "dimensions", Description: "Set the maximum output width and height",
					Options: []discord.ApplicationCommandOption{
						discord.ApplicationCommandOptionInt{Name: "width", Description: "Maximum width in pixels", Required: true,
							MinValue: ptr(config.MinDimension), MaxValue: ptr(config.MaxDimension)},
						discord.ApplicationCommandOptionInt{Name: "height", Description: "Maximum height in pixels", Required: true,
							MinValue: ptr(config.MinDimension), MaxValue: ptr(config.MaxDimension)},
					},
				},
				discord.ApplicationCommandOptionSubCommand{
					Name: "max-file-size", Description: "Limit the output file size",
					Options: []discord.ApplicationCommandOption{
						discord.ApplicationCommandOptionBool{Name: "enabled", Description: "Enable the limit", Required: true},
						discord.ApplicationCommandOptionFloat{Name: "size-mib", Description: "Maximum size in MiB (required when enabling)",
							MinValue: ptr(0.1), MaxValue: ptr(float64(config.MaxFileSizeLimit >> 20))},
					},
				},
				discord.ApplicationCommandOptionSubCommand{Name: "preserve-alpha", Description: "Keep transparency (outputs PNG)",
					Options: enabled("Keep transparency")},
				discord.ApplicationCommandOptionSubCommand{
					Name: "jpeg-quality", Description: "Set the JPEG quality",
					Options: []discord.ApplicationCommandOption{
						discord.ApplicationCommandOptionInt{Name: "quality", Description: "1–100", Required: true,
							MinValue: ptr(config.MinJPEGQuality), MaxValue: ptr(config.MaxJPEGQuality)},
					},
				},
				discord.ApplicationCommandOptionSubCommand{Name: "strip-metadata", Description: "Remove EXIF/GPS metadata from outputs",
					Options: enabled("Strip metadata")},
				discord.ApplicationCommandOptionSubCommand{
					Name: "delete-original", Description: "Delete the user's original message after conversion",
					Options: enabled("Delete the original message"),
				},
				discord.ApplicationCommandOptionSubCommand{Name: "reset", Description: "Restore default settings"},
			},
		},
	}
}

// commandPath returns e.g. "channel add" for /config channel add.
func commandPath(data discord.SlashCommandInteractionData) string {
	var parts []string
	if data.SubCommandGroupName != nil {
		parts = append(parts, *data.SubCommandGroupName)
	}
	if data.SubCommandName != nil {
		parts = append(parts, *data.SubCommandName)
	}
	return strings.Join(parts, " ")
}

func ephemeral(content string) discord.MessageCreate {
	return discord.MessageCreate{Content: content, Flags: discord.MessageFlagEphemeral, AllowedMentions: noMentions}
}

const msgNoPermission = "You need the **Manage Server** permission to change the image converter configuration."

// handleConfig executes a /config subcommand. It re-checks Manage Guild itself because server
// admins can override a command's default member permissions in the integration settings.
func (b *Bot) handleConfig(ctx context.Context, guildID snowflake.ID, perms discord.Permissions, data discord.SlashCommandInteractionData) discord.MessageCreate {
	if !perms.Has(discord.PermissionManageGuild) {
		return ephemeral(msgNoPermission)
	}
	path := commandPath(data)
	log := b.logger.With(slog.String("guild_id", guildID.String()), slog.String("command", "/config "+path))

	reply, err := b.runConfig(ctx, guildID, path, data)
	if err != nil {
		if errors.Is(err, config.ErrInvalid) {
			return ephemeral(strings.TrimPrefix(err.Error(), config.ErrInvalid.Error()+": ") + ".")
		}
		log.Error("config command failed", slog.Any("err", err))
		return ephemeral("Failed to update the configuration. Please try again later.")
	}
	log.Info("config command executed")
	return reply
}

func (b *Bot) runConfig(ctx context.Context, guildID snowflake.ID, path string, data discord.SlashCommandInteractionData) (discord.MessageCreate, error) {
	update := func(mutate func(*config.Config), done string) (discord.MessageCreate, error) {
		if _, err := b.configs.Update(ctx, guildID, mutate); err != nil {
			return discord.MessageCreate{}, err
		}
		return ephemeral(done), nil
	}

	switch path {
	case "show":
		cfg, err := b.configs.Get(ctx, guildID)
		if err != nil {
			return discord.MessageCreate{}, err
		}
		m := ephemeral("")
		m.Embeds = []discord.Embed{configEmbed(cfg)}
		return m, nil

	case "channel add":
		channelID := data.Snowflake("channel")
		added, err := b.configs.AddChannel(ctx, guildID, channelID)
		if err != nil {
			return discord.MessageCreate{}, err
		}
		if !added {
			return ephemeral("Channel is already enabled."), nil
		}
		return ephemeral(fmt.Sprintf("Automatic conversion enabled in <#%s>.", channelID)), nil

	case "channel remove":
		channelID := data.Snowflake("channel")
		removed, err := b.configs.RemoveChannel(ctx, guildID, channelID)
		if err != nil {
			return discord.MessageCreate{}, err
		}
		if !removed {
			return ephemeral("Channel is not enabled."), nil
		}
		return ephemeral(fmt.Sprintf("Automatic conversion disabled in <#%s>.", channelID)), nil

	case "channel list":
		cfg, err := b.configs.Get(ctx, guildID)
		if err != nil {
			return discord.MessageCreate{}, err
		}
		if len(cfg.Channels) == 0 {
			return ephemeral("No channels have automatic conversion enabled."), nil
		}
		return ephemeral("Automatic conversion is enabled in:\n" + channelList(cfg.Channels)), nil

	case "dimensions":
		w, h := data.Int("width"), data.Int("height")
		return update(func(c *config.Config) { c.MaxWidth, c.MaxHeight = w, h },
			fmt.Sprintf("Maximum dimensions set to %d × %d px.", w, h))

	case "max-file-size":
		if !data.Bool("enabled") {
			return update(func(c *config.Config) { c.MaxFileSizeBytes = nil }, "Maximum output file size disabled.")
		}
		mib, ok := data.OptFloat("size-mib")
		if !ok {
			return ephemeral("Please provide `size-mib` when enabling the file-size limit."), nil
		}
		size := config.MebibytesToBytes(mib)
		return update(func(c *config.Config) { c.MaxFileSizeBytes = &size },
			fmt.Sprintf("Maximum output file size set to %s MiB.", formatMiB(size)))

	case "preserve-alpha":
		v := data.Bool("enabled")
		return update(func(c *config.Config) { c.PreserveAlpha = v }, "Preserve transparency "+onOff(v)+".")

	case "jpeg-quality":
		q := data.Int("quality")
		return update(func(c *config.Config) { c.JPEGQuality = q }, fmt.Sprintf("JPEG quality set to %d.", q))

	case "strip-metadata":
		v := data.Bool("enabled")
		return update(func(c *config.Config) { c.StripMetadata = v }, "Strip metadata "+onOff(v)+".")

	case "delete-original":
		v := data.Bool("enabled")
		return update(func(c *config.Config) { c.DeleteOriginal = v },
			"Delete original message after conversion "+onOff(v)+".")

	case "reset":
		m := ephemeral("Reset configuration?\nThis restores all default settings and **disables automatic conversion in every channel**.")
		m.Components = []discord.LayoutComponent{discord.NewActionRow(
			discord.NewDangerButton("Confirm", resetConfirmID),
			discord.NewSecondaryButton("Cancel", resetCancelID),
		)}
		return m, nil
	}
	return discord.MessageCreate{}, fmt.Errorf("unknown subcommand %q", path)
}

// handleResetButton finishes the /config reset confirmation.
func (b *Bot) handleResetButton(ctx context.Context, guildID snowflake.ID, perms discord.Permissions, confirmed bool) discord.MessageUpdate {
	done := func(text string) discord.MessageUpdate {
		empty := []discord.LayoutComponent{}
		return discord.MessageUpdate{Content: &text, Components: &empty}
	}
	if !confirmed {
		return done("Reset cancelled.")
	}
	if !perms.Has(discord.PermissionManageGuild) {
		return done(msgNoPermission)
	}
	if _, err := b.configs.Reset(ctx, guildID); err != nil {
		b.logger.Error("reset config", slog.String("guild_id", guildID.String()), slog.Any("err", err))
		return done("Failed to reset the configuration. Please try again later.")
	}
	b.logger.Info("config reset", slog.String("guild_id", guildID.String()))
	return done("Configuration reset to defaults.")
}

func configEmbed(cfg config.Config) discord.Embed {
	channels := "None"
	if len(cfg.Channels) > 0 {
		channels = channelList(cfg.Channels)
	}
	maxSize := "Disabled"
	if cfg.MaxFileSizeBytes != nil {
		maxSize = formatMiB(*cfg.MaxFileSizeBytes) + " MiB"
	}
	return discord.Embed{
		Title: "Image Converter Configuration",
		Fields: []discord.EmbedField{
			{Name: "Channels", Value: channels},
			{Name: "Maximum dimensions", Value: fmt.Sprintf("%d × %d px", cfg.MaxWidth, cfg.MaxHeight)},
			{Name: "Maximum output file size", Value: maxSize},
			{Name: "JPEG quality", Value: fmt.Sprint(cfg.JPEGQuality)},
			{Name: "Preserve transparency", Value: enabledDisabled(cfg.PreserveAlpha)},
			{Name: "Strip metadata", Value: enabledDisabled(cfg.StripMetadata)},
			{Name: "Delete original message", Value: enabledDisabled(cfg.DeleteOriginal)},
		},
	}
}

func channelList(ids []snowflake.ID) string {
	lines := make([]string, len(ids))
	for i, id := range ids {
		lines[i] = "<#" + id.String() + ">"
	}
	return strings.Join(lines, "\n")
}

func formatMiB(bytes int64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", float64(bytes)/(1<<20)), "0"), ".")
}

func onOff(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}

func enabledDisabled(v bool) string {
	if v {
		return "Enabled"
	}
	return "Disabled"
}
