package bot

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"

	"github.com/nekogravitycat/image-converter-bot/internal/discordurl"
	"github.com/nekogravitycat/image-converter-bot/internal/imageproc"
)

var (
	errUpload         = errors.New("upload failed")
	errUploadTooLarge = errors.New("upload rejected as too large")
)

const msgQueueFull = "Image processing queue is currently full. Please try again later."

// userMessage maps internal errors to text safe to show in Discord; details stay in the logs.
func userMessage(err error) string {
	switch {
	case errors.Is(err, imageproc.ErrUnsupportedFormat):
		return "Unsupported image format."
	case errors.Is(err, imageproc.ErrTooLarge), errors.Is(err, errInputTooLarge):
		return "The image is too large to process safely."
	case errors.Is(err, imageproc.ErrDecode):
		return "Failed to decode the image."
	case errors.Is(err, imageproc.ErrFileSizeLimit):
		return "Unable to reduce the image below the configured file-size limit."
	case errors.Is(err, errUploadTooLarge):
		return "The converted image is too large to upload to Discord."
	case errors.Is(err, errUpload):
		return "Failed to upload the converted image."
	case errors.Is(err, errDownload):
		return "Failed to download the image from Discord."
	case errors.Is(err, context.DeadlineExceeded):
		return "Image processing took too long and was cancelled."
	default:
		return "Something went wrong while converting the image."
	}
}

// classifyUploadError distinguishes Discord's payload-too-large rejection from other failures.
func classifyUploadError(err error) error {
	var restErr *rest.Error
	if rest.IsJSONErrorCode(err, rest.JSONErrorCodeRequestEntityTooLarge) ||
		(errors.As(err, &restErr) && restErr.Response != nil && restErr.Response.StatusCode == http.StatusRequestEntityTooLarge) {
		return fmt.Errorf("%w: %w", errUploadTooLarge, err)
	}
	return fmt.Errorf("%w: %w", errUpload, err)
}

// convertAttachment downloads and processes one attachment with the guild's options.
func (b *Bot) convertAttachment(ctx context.Context, att discord.Attachment, opts imageproc.Options) (*imageproc.Result, int, error) {
	if int64(att.Size) > b.settings.MaxInputFileSize {
		return nil, 0, fmt.Errorf("%w: attachment reports %d bytes", errInputTooLarge, att.Size)
	}
	data, err := download(ctx, b.http, b.allowURL, att.URL, b.settings.MaxInputFileSize)
	if err != nil {
		return nil, 0, err
	}
	pctx, cancel := context.WithTimeout(ctx, b.settings.ProcessTimeout)
	defer cancel()
	res, err := b.proc.Process(pctx, data, opts)
	if err != nil {
		return nil, len(data), fmt.Errorf("process image: %w", err)
	}
	return res, len(data), nil
}

// outputFile names the upload randomly; the user's filename is never trusted for output.
func outputFile(res *imageproc.Result) *discord.File {
	var id [3]byte
	_, _ = rand.Read(id[:])
	return discord.NewFile("converted-"+hex.EncodeToString(id[:])+res.Format.Ext(), "", bytes.NewReader(res.Data))
}

func resultSummary(res *imageproc.Result) string {
	return discordurl.Summary(res.InputWidth, res.InputHeight, res.OutputWidth, res.OutputHeight, string(res.Format), len(res.Data))
}

// finalizeResult reads the signed URL Discord assigned to the uploaded file and edits the
// result message to show it, its expiry and the Refresh URL button. The upload already
// succeeded, so failures here are retried a few times and then only logged.
func (b *Bot) finalizeResult(ctx context.Context, msg *discord.Message, summary string, edit func(context.Context, discord.MessageUpdate) error, log *slog.Logger) {
	if len(msg.Attachments) == 0 {
		log.Error("uploaded result message has no attachment", slog.String("result_message_id", msg.ID.String()))
		return
	}
	att := msg.Attachments[0]
	expiresAt, err := discordurl.ParseExpiry(att.URL)
	if err != nil {
		log.Warn("cannot parse attachment URL expiry", slog.String("attachment_id", att.ID.String()), slog.Any("err", err))
	}
	content := discordurl.ResultContent(summary, att.URL, expiresAt)
	components := discordurl.Components()
	update := discord.MessageUpdate{Content: &content, Components: &components}

	const attempts = 3
	for i := range attempts {
		ectx, cancel := b.apiCtx(ctx)
		err = edit(ectx, update)
		cancel()
		if err == nil {
			return
		}
		log.Warn("edit result message failed", slog.Int("attempt", i+1), slog.Any("err", err))
		if i < attempts-1 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(i+1) * time.Second):
			}
		}
	}
	log.Error("giving up adding URL to result message", slog.String("result_message_id", msg.ID.String()), slog.Any("err", err))
}

// displayName makes an untrusted filename safe to echo inside inline code.
func displayName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if b.Len() >= 64 {
			b.WriteString("…")
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "image"
	}
	return b.String()
}

func logConverted(log *slog.Logger, res *imageproc.Result, inputBytes int, start time.Time) {
	log.Info("image converted",
		slog.String("event", "image_converted"),
		slog.String("input_format", res.InputFormat),
		slog.String("output_format", string(res.Format)),
		slog.Int("input_width", res.InputWidth),
		slog.Int("input_height", res.InputHeight),
		slog.Int("output_width", res.OutputWidth),
		slog.Int("output_height", res.OutputHeight),
		slog.Int("input_bytes", inputBytes),
		slog.Int("output_bytes", len(res.Data)),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
	)
}
