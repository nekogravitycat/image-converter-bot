// Package bot wires Discord events to the config service and the image pipeline.
package bot

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/nekogravitycat/image-converter-bot/internal/config"
	"github.com/nekogravitycat/image-converter-bot/internal/imageproc"
	"github.com/nekogravitycat/image-converter-bot/internal/worker"
)

// discordAPI is the subset of the Discord REST API the bot uses; rest.Rest satisfies it and tests fake it.
type discordAPI interface {
	CreateMessage(channelID snowflake.ID, messageCreate discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error)
	UpdateMessage(channelID snowflake.ID, messageID snowflake.ID, messageUpdate discord.MessageUpdate, opts ...rest.RequestOpt) (*discord.Message, error)
	DeleteMessage(channelID snowflake.ID, messageID snowflake.ID, opts ...rest.RequestOpt) error
	GetMessage(channelID snowflake.ID, messageID snowflake.ID, opts ...rest.RequestOpt) (*discord.Message, error)
	GetInteractionResponse(applicationID snowflake.ID, interactionToken string, opts ...rest.RequestOpt) (*discord.Message, error)
	UpdateInteractionResponse(applicationID snowflake.ID, interactionToken string, messageUpdate discord.MessageUpdate, opts ...rest.RequestOpt) (*discord.Message, error)
	CreateFollowupMessage(applicationID snowflake.ID, interactionToken string, messageCreate discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error)
	AddReaction(channelID snowflake.ID, messageID snowflake.ID, emoji string, opts ...rest.RequestOpt) error
	RemoveOwnReaction(channelID snowflake.ID, messageID snowflake.ID, emoji string, opts ...rest.RequestOpt) error
}

type imageProcessor interface {
	Process(ctx context.Context, data []byte, opts imageproc.Options) (*imageproc.Result, error)
}

type jobQueue interface {
	TrySubmit(job worker.Job) bool
}

// Settings are infrastructure limits, not per-guild configuration.
type Settings struct {
	MaxInputFileSize int64
	ProcessTimeout   time.Duration // image decode/encode only
	JobTimeout       time.Duration // whole job: download, process, upload, edit
	APITimeout       time.Duration // single Discord REST call
}

// Bot handles gateway messages and interactions; conversions run on the job queue.
type Bot struct {
	api       discordAPI
	configs   *config.Service
	queue     jobQueue
	proc      imageProcessor
	http      *http.Client
	allowURL  func(*url.URL) bool // download host policy; tests swap in httptest servers
	settings  Settings
	logger    *slog.Logger
	reactions *reactionTracker
}

// New returns a Bot that talks to Discord through api and converts images with proc.
func New(api discordAPI, configs *config.Service, queue jobQueue, proc imageProcessor, settings Settings, logger *slog.Logger) *Bot {
	return &Bot{
		api:       api,
		configs:   configs,
		queue:     queue,
		proc:      proc,
		http:      newDownloadClient(),
		allowURL:  isDiscordCDN,
		settings:  settings,
		logger:    logger,
		reactions: newReactionTracker(),
	}
}

// apiCtx bounds a single Discord REST call.
func (b *Bot) apiCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, b.settings.APITimeout)
}

var noMentions = &discord.AllowedMentions{}

func toOptions(cfg config.Config) imageproc.Options {
	opts := imageproc.Options{
		MaxWidth:      cfg.MaxWidth,
		MaxHeight:     cfg.MaxHeight,
		JPEGQuality:   cfg.JPEGQuality,
		PreserveAlpha: cfg.PreserveAlpha,
		StripMetadata: cfg.StripMetadata,
	}
	if cfg.MaxFileSizeBytes != nil {
		opts.MaxFileSizeBytes = *cfg.MaxFileSizeBytes
	}
	return opts
}
