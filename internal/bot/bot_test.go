package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/nekogravitycat/image-converter-bot/internal/config"
	"github.com/nekogravitycat/image-converter-bot/internal/discordurl"
	"github.com/nekogravitycat/image-converter-bot/internal/imageproc"
)

const (
	guildID   snowflake.ID = 1
	channelID snowflake.ID = 10
	otherChan snowflake.ID = 11
)

func imageAttachment(id snowflake.ID, name, url string) discord.Attachment {
	ct := "image/jpeg"
	return discord.Attachment{ID: id, Filename: name, ContentType: &ct, Size: 100, URL: url}
}

func sourceMessage(attachments ...discord.Attachment) discord.Message {
	return discord.Message{ID: 500, ChannelID: channelID, Author: discord.User{ID: 7}, Attachments: attachments}
}

func (e *testEnv) allow(t *testing.T, ch snowflake.ID) {
	t.Helper()
	if _, err := e.configs.AddChannel(context.Background(), guildID, ch); err != nil {
		t.Fatal(err)
	}
}

// ---- message handler ----

func TestBotMessageIgnored(t *testing.T) {
	env := newTestEnv(t)
	env.allow(t, channelID)
	msg := sourceMessage(imageAttachment(1, "a.jpg", ""))
	msg.Author.Bot = true
	env.bot.handleMessage(context.Background(), guildID, msg)

	webhook := sourceMessage(imageAttachment(1, "a.jpg", ""))
	webhook.WebhookID = new(snowflake.ID)
	env.bot.handleMessage(context.Background(), guildID, webhook)

	if len(env.queue.jobs) != 0 {
		t.Fatalf("bot/webhook message queued %d jobs", len(env.queue.jobs))
	}
}

func TestNonAllowedChannelIgnored(t *testing.T) {
	env := newTestEnv(t)
	env.allow(t, otherChan)
	env.bot.handleMessage(context.Background(), guildID, sourceMessage(imageAttachment(1, "a.jpg", "")))
	if len(env.queue.jobs) != 0 {
		t.Fatal("message in non-allowlisted channel was queued")
	}
}

func TestAllowedChannelImageAccepted(t *testing.T) {
	env := newTestEnv(t)
	env.allow(t, channelID)
	env.bot.handleMessage(context.Background(), guildID, sourceMessage(imageAttachment(1, "IMG_1234.HEIC", "")))
	if len(env.queue.jobs) != 1 {
		t.Fatalf("queued %d jobs, want 1", len(env.queue.jobs))
	}
}

func TestMultipleAttachmentsCreateMultipleJobs(t *testing.T) {
	env := newTestEnv(t)
	env.allow(t, channelID)
	env.bot.handleMessage(context.Background(), guildID, sourceMessage(
		imageAttachment(1, "image1.heic", ""),
		imageAttachment(2, "image2.png", ""),
		imageAttachment(3, "image3.jpg", ""),
	))
	if len(env.queue.jobs) != 3 {
		t.Fatalf("queued %d jobs, want 3", len(env.queue.jobs))
	}
}

func TestNonImageAttachmentIgnored(t *testing.T) {
	env := newTestEnv(t)
	env.allow(t, channelID)
	pdf := "application/pdf"
	env.bot.handleMessage(context.Background(), guildID, sourceMessage(
		discord.Attachment{ID: 1, Filename: "notes.pdf", ContentType: &pdf},
		discord.Attachment{ID: 2, Filename: "archive.zip"},
		// No content type but an image extension still counts; bytes decide later.
		discord.Attachment{ID: 3, Filename: "photo.HEIC"},
	))
	if len(env.queue.jobs) != 1 {
		t.Fatalf("queued %d jobs, want 1 (only the HEIC)", len(env.queue.jobs))
	}
}

func TestQueueFullRepliesOnce(t *testing.T) {
	env := newTestEnv(t)
	env.allow(t, channelID)
	env.queue.cap = 1
	env.api.notify = make(chan string, 4)
	env.bot.handleMessage(context.Background(), guildID, sourceMessage(
		imageAttachment(1, "a.jpg", ""), imageAttachment(2, "b.jpg", ""), imageAttachment(3, "c.jpg", ""),
	))
	select {
	case <-env.api.notify:
	case <-time.After(2 * time.Second):
		t.Fatal("no queue-full reply sent")
	}
	time.Sleep(50 * time.Millisecond)
	// One attachment fit in the queue (so its "queued" reaction was added) and one reply
	// was sent for the two that didn't.
	if n := len(env.api.callNames()); n != 2 || env.api.creates[0].Content != msgQueueFull {
		t.Fatalf("calls = %v, want a queue-full reply plus one reaction", env.api.callNames())
	}
	if len(env.api.reactionsAdded) != 1 || env.api.reactionsAdded[0] != channelID.String()+":500:"+emojiQueued {
		t.Fatalf("reactionsAdded = %v", env.api.reactionsAdded)
	}
}

// ---- full auto job with fakes ----

func cdnServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(body) }))
	t.Cleanup(srv.Close)
	return srv
}

func TestAutoJobUploadsThenEditsInURL(t *testing.T) {
	env := newTestEnv(t)
	env.allow(t, channelID)
	srv := cdnServer(t, []byte("source-bytes"))
	env.bot.allowURL = func(*url.URL) bool { return true }
	env.proc.result = &imageproc.Result{Data: []byte("jpeg"), Format: imageproc.FormatJPEG, InputFormat: "heif",
		InputWidth: 3024, InputHeight: 4032, OutputWidth: 1125, OutputHeight: 1500}

	signed := "https://cdn.discordapp.com/attachments/10/900/converted-a8f31c.jpg?ex=6ABCD123&is=1&hm=2"
	env.api.createFn = func(m discord.MessageCreate) (*discord.Message, error) {
		return &discord.Message{ID: 900, Content: m.Content, Attachments: []discord.Attachment{{ID: 77, URL: signed}}}, nil
	}

	env.bot.handleMessage(context.Background(), guildID, sourceMessage(imageAttachment(1, "IMG_1234.HEIC", srv.URL)))
	if len(env.queue.jobs) != 1 {
		t.Fatal("job not queued")
	}
	env.queue.jobs[0](context.Background())

	if string(env.proc.got) != "source-bytes" {
		t.Fatalf("processor got %q", env.proc.got)
	}
	if env.proc.opts.MaxWidth != config.DefaultMaxWidth || !env.proc.opts.StripMetadata {
		t.Fatalf("processor got wrong options: %+v", env.proc.opts)
	}
	// Reaction lifecycle: queued on receipt, swapped to processing once the job starts,
	// cleared once it finishes -- interleaved with the usual reply/edit calls.
	wantCalls := "AddReaction,AddReaction,RemoveOwnReaction,CreateMessage,UpdateMessage,RemoveOwnReaction"
	if got := env.api.callNames(); strings.Join(got, ",") != wantCalls {
		t.Fatalf("calls = %v, want %s", got, wantCalls)
	}
	msg500 := channelID.String() + ":500:"
	if got := env.api.reactionsAdded; len(got) != 2 || got[0] != msg500+emojiQueued || got[1] != msg500+emojiProcessing {
		t.Fatalf("reactionsAdded = %v", got)
	}
	if got := env.api.reactionsRemoved; len(got) != 2 || got[0] != msg500+emojiQueued || got[1] != msg500+emojiProcessing {
		t.Fatalf("reactionsRemoved = %v", got)
	}
	create := env.api.creates[0]
	if len(create.Files) != 1 || !strings.HasPrefix(create.Files[0].Name, "converted-") || !strings.HasSuffix(create.Files[0].Name, ".jpg") {
		t.Fatalf("unexpected upload: %+v", create.Files)
	}
	if create.MessageReference == nil || *create.MessageReference.MessageID != 500 {
		t.Fatal("result is not a reply to the source message")
	}
	update := env.api.updates[0]
	want := discordurl.ResultContent("Converted: 3024×4032 → 1125×1500 · JPEG · 4 B", signed, time.Unix(0x6ABCD123, 0))
	if *update.Content != want {
		t.Fatalf("edited content =\n%s\nwant\n%s", *update.Content, want)
	}
	if update.Components == nil || len(*update.Components) != 1 {
		t.Fatal("Refresh URL button missing")
	}
	if update.Files != nil || update.Attachments != nil {
		t.Fatal("finalize edit must not touch attachments")
	}
}

func TestAutoJobDeletesOriginalWhenConfigured(t *testing.T) {
	env := newTestEnv(t)
	env.allow(t, channelID)
	if _, err := env.configs.Update(context.Background(), guildID, func(c *config.Config) { c.DeleteOriginal = true }); err != nil {
		t.Fatal(err)
	}
	srv := cdnServer(t, []byte("source-bytes"))
	env.bot.allowURL = func(*url.URL) bool { return true }
	env.proc.result = &imageproc.Result{Data: []byte("jpeg"), Format: imageproc.FormatJPEG}
	env.api.createFn = func(m discord.MessageCreate) (*discord.Message, error) {
		return &discord.Message{ID: 900, Content: m.Content, Attachments: []discord.Attachment{{ID: 77, URL: srv.URL}}}, nil
	}

	env.bot.handleMessage(context.Background(), guildID, sourceMessage(imageAttachment(1, "a.jpg", srv.URL)))
	env.queue.jobs[0](context.Background())

	create := env.api.creates[0]
	if create.MessageReference != nil {
		t.Fatal("result must not be a reply when the original will be deleted")
	}
	if len(env.api.deletes) != 1 || env.api.deletes[0] != channelID.String()+":500" {
		t.Fatalf("deletes = %v, want the source message deleted once", env.api.deletes)
	}
}

func TestAutoJobKeepsOriginalOnFailureEvenWhenConfigured(t *testing.T) {
	env := newTestEnv(t)
	env.allow(t, channelID)
	if _, err := env.configs.Update(context.Background(), guildID, func(c *config.Config) { c.DeleteOriginal = true }); err != nil {
		t.Fatal(err)
	}
	srv := cdnServer(t, []byte("junk"))
	env.bot.allowURL = func(*url.URL) bool { return true }
	env.proc.err = fmt.Errorf("wrapped: %w", imageproc.ErrUnsupportedFormat)

	env.bot.handleMessage(context.Background(), guildID, sourceMessage(imageAttachment(1, "a.jpg", srv.URL)))
	env.queue.jobs[0](context.Background())

	if len(env.api.deletes) != 0 {
		t.Fatal("original message deleted despite a failed conversion")
	}
	if len(env.api.creates) != 1 || env.api.creates[0].MessageReference == nil {
		t.Fatal("error reply must still be a reply to the source message")
	}
}

func TestReactionsClearOnlyAfterAllAttachmentsDone(t *testing.T) {
	env := newTestEnv(t)
	env.allow(t, channelID)
	srv := cdnServer(t, []byte("bytes"))
	env.bot.allowURL = func(*url.URL) bool { return true }
	env.proc.result = &imageproc.Result{Data: []byte("jpeg"), Format: imageproc.FormatJPEG}

	env.bot.handleMessage(context.Background(), guildID, sourceMessage(
		imageAttachment(1, "a.jpg", srv.URL), imageAttachment(2, "b.jpg", srv.URL),
	))
	if len(env.queue.jobs) != 2 {
		t.Fatalf("queued %d jobs, want 2", len(env.queue.jobs))
	}
	msg500 := channelID.String() + ":500:"
	if got := env.api.reactionsAdded; len(got) != 1 || got[0] != msg500+emojiQueued {
		t.Fatalf("reactionsAdded after queueing = %v", got)
	}

	env.queue.jobs[0](context.Background())
	// The first job running swaps queued -> processing (removing ⏳), but the processing
	// reaction itself must survive since the second attachment is still in flight.
	if got := env.api.reactionsRemoved; len(got) != 1 || got[0] != msg500+emojiQueued {
		t.Fatalf("reactionsRemoved after first job started = %v", got)
	}
	if got := env.api.reactionsAdded; len(got) != 2 || got[1] != msg500+emojiProcessing {
		t.Fatalf("reactionsAdded after first job started = %v", got)
	}

	env.queue.jobs[1](context.Background())
	if got := env.api.reactionsRemoved; len(got) != 2 || got[1] != msg500+emojiProcessing {
		t.Fatalf("reactionsRemoved after both attachments finished = %v", got)
	}
}

func TestAutoJobReportsProcessingError(t *testing.T) {
	env := newTestEnv(t)
	srv := cdnServer(t, []byte("junk"))
	env.bot.allowURL = func(*url.URL) bool { return true }
	env.proc.err = fmt.Errorf("wrapped: %w", imageproc.ErrUnsupportedFormat)

	env.bot.runAutoJob(context.Background(), guildID, channelID, 500, imageAttachment(1, "we`ird name.jpg", srv.URL), env.bot.logger)
	if len(env.api.creates) != 1 {
		t.Fatalf("calls = %v", env.api.callNames())
	}
	if got := env.api.creates[0].Content; got != "`we_ird_name.jpg`: Unsupported image format." {
		t.Fatalf("error reply = %q", got)
	}
}

func TestAutoJobUploadTooLarge(t *testing.T) {
	env := newTestEnv(t)
	srv := cdnServer(t, []byte("x"))
	env.bot.allowURL = func(*url.URL) bool { return true }
	env.proc.result = &imageproc.Result{Data: []byte("big"), Format: imageproc.FormatPNG}
	env.api.createFn = func(m discord.MessageCreate) (*discord.Message, error) {
		if len(m.Files) > 0 {
			return nil, &rest.Error{Response: &http.Response{StatusCode: http.StatusRequestEntityTooLarge}}
		}
		return &discord.Message{}, nil
	}
	env.bot.runAutoJob(context.Background(), guildID, channelID, 500, imageAttachment(1, "a.png", srv.URL), env.bot.logger)
	last := env.api.creates[len(env.api.creates)-1].Content
	if !strings.HasSuffix(last, "The converted image is too large to upload to Discord.") {
		t.Fatalf("reply = %q", last)
	}
}

// ---- refresh button ----

func TestRefreshEditsSameMessage(t *testing.T) {
	env := newTestEnv(t)
	fresh := "https://cdn.discordapp.com/attachments/10/900/converted-a8f31c.jpg?ex=6B000000&is=1&hm=new"
	old := discordurl.ResultContent("Converted: 10×10 → 10×10 · PNG · 1 KiB", "https://cdn.discordapp.com/old?ex=1", time.Unix(1, 0))
	env.api.getInteractionFn = func() (*discord.Message, error) {
		return &discord.Message{ID: 900, Content: old, Attachments: []discord.Attachment{{ID: 77, URL: fresh}}}, nil
	}

	if err := env.bot.refreshURL(context.Background(), refreshTarget{appID: 1, token: "t", channelID: channelID, messageID: 900}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(env.api.callNames(), ","); got != "GetInteractionResponse,UpdateInteractionResponse" {
		t.Fatalf("calls = %s (no new messages or uploads allowed)", got)
	}
	u := env.api.interactionUpdates[0]
	want := discordurl.ResultContent("Converted: 10×10 → 10×10 · PNG · 1 KiB", fresh, time.Unix(0x6B000000, 0))
	if *u.Content != want {
		t.Fatalf("content =\n%s\nwant\n%s", *u.Content, want)
	}
	if u.Files != nil || u.Attachments != nil {
		t.Fatal("refresh must not upload or replace attachments")
	}
}

func TestRefreshFallsBackToChannelFetch(t *testing.T) {
	env := newTestEnv(t)
	env.api.getInteractionFn = func() (*discord.Message, error) { return nil, errors.New("unknown webhook") }
	env.api.getMessageFn = func() (*discord.Message, error) {
		return &discord.Message{Attachments: []discord.Attachment{{URL: "https://cdn.discordapp.com/a.jpg?ex=6B000000"}}}, nil
	}
	if err := env.bot.refreshURL(context.Background(), refreshTarget{}); err != nil {
		t.Fatal(err)
	}
	if len(env.api.interactionUpdates) != 1 {
		t.Fatal("message not edited after fallback fetch")
	}
}

func TestRefreshFailuresLeaveMessageUntouched(t *testing.T) {
	cases := map[string]struct {
		msg  *discord.Message
		err  error
		want error
	}{
		"deleted":       {nil, errors.New("unknown message"), errRefreshFetch},
		"no attachment": {&discord.Message{}, nil, errRefreshNoAttachment},
		"no expiry":     {&discord.Message{Attachments: []discord.Attachment{{URL: "https://cdn.discordapp.com/a.jpg"}}}, nil, errRefreshExpiry},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := newTestEnv(t)
			env.api.getInteractionFn = func() (*discord.Message, error) { return c.msg, c.err }
			env.api.getMessageFn = func() (*discord.Message, error) { return c.msg, c.err }
			err := env.bot.refreshURL(context.Background(), refreshTarget{})
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if len(env.api.interactionUpdates) != 0 || len(env.api.updates) != 0 {
				t.Fatal("message was edited despite failure")
			}
			if refreshErrorMessage(err) == "" {
				t.Fatal("empty user-facing error")
			}
		})
	}
}

// ---- /config ----

func slashData(sub string, group *string, opts map[string]any) discord.SlashCommandInteractionData {
	d := discord.SlashCommandInteractionData{SubCommandName: &sub, SubCommandGroupName: group, Options: map[string]discord.SlashCommandOption{}}
	for name, v := range opts {
		raw, _ := json.Marshal(v)
		d.Options[name] = discord.SlashCommandOption{Name: name, Value: raw}
	}
	return d
}

var manageGuild = discord.PermissionManageGuild | discord.PermissionSendMessages

func TestConfigRequiresManageGuild(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	data := slashData("dimensions", nil, map[string]any{"width": 2048, "height": 2048})

	resp := env.bot.handleConfig(ctx, guildID, discord.PermissionSendMessages, data)
	if resp.Content != msgNoPermission || resp.Flags&discord.MessageFlagEphemeral == 0 {
		t.Fatalf("unprivileged response = %+v", resp)
	}
	if cfg, _ := env.configs.Get(ctx, guildID); cfg.MaxWidth != config.DefaultMaxWidth {
		t.Fatal("unprivileged user changed config")
	}

	resp = env.bot.handleConfig(ctx, guildID, manageGuild, data)
	if resp.Flags&discord.MessageFlagEphemeral == 0 {
		t.Fatal("config responses must be ephemeral")
	}
	if cfg, _ := env.configs.Get(ctx, guildID); cfg.MaxWidth != 2048 || cfg.MaxHeight != 2048 {
		t.Fatalf("privileged update not applied: %+v (%q)", cfg, resp.Content)
	}
}

func TestResetButtonRequiresManageGuild(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.allow(t, channelID)

	env.bot.handleResetButton(ctx, guildID, discord.PermissionSendMessages, true)
	if ok, _ := env.configs.IsChannelAllowed(ctx, guildID, channelID); !ok {
		t.Fatal("unprivileged reset took effect")
	}
	env.bot.handleResetButton(ctx, guildID, manageGuild, false)
	if ok, _ := env.configs.IsChannelAllowed(ctx, guildID, channelID); !ok {
		t.Fatal("cancelled reset took effect")
	}
	u := env.bot.handleResetButton(ctx, guildID, manageGuild, true)
	if ok, _ := env.configs.IsChannelAllowed(ctx, guildID, channelID); ok {
		t.Fatal("confirmed reset did not clear channels")
	}
	if u.Components == nil || len(*u.Components) != 0 {
		t.Fatal("reset buttons should be removed after use")
	}
}

func TestConfigCommands(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	group := "channel"
	run := func(sub string, g *string, opts map[string]any) string {
		return env.bot.handleConfig(ctx, guildID, manageGuild, slashData(sub, g, opts)).Content
	}

	if got := run("add", &group, map[string]any{"channel": "10"}); !strings.Contains(got, "<#10>") {
		t.Fatalf("channel add = %q", got)
	}
	if got := run("add", &group, map[string]any{"channel": "10"}); got != "Channel is already enabled." {
		t.Fatalf("duplicate channel add = %q", got)
	}
	if got := run("list", &group, nil); !strings.Contains(got, "<#10>") {
		t.Fatalf("channel list = %q", got)
	}
	if got := run("max-file-size", nil, map[string]any{"enabled": true}); !strings.Contains(got, "size-mib") {
		t.Fatalf("enable without size = %q", got)
	}
	run("max-file-size", nil, map[string]any{"enabled": true, "size-mib": 4})
	if cfg, _ := env.configs.Get(ctx, guildID); cfg.MaxFileSizeBytes == nil || *cfg.MaxFileSizeBytes != 4<<20 {
		t.Fatalf("max file size not stored: %+v", cfg.MaxFileSizeBytes)
	}
	run("max-file-size", nil, map[string]any{"enabled": false})
	if cfg, _ := env.configs.Get(ctx, guildID); cfg.MaxFileSizeBytes != nil {
		t.Fatal("disabling max file size did not store NULL")
	}
	if got := run("jpeg-quality", nil, map[string]any{"quality": 101}); !strings.Contains(got, "between 1 and 100") {
		t.Fatalf("out-of-range quality = %q", got)
	}
	run("preserve-alpha", nil, map[string]any{"enabled": false})
	run("strip-metadata", nil, map[string]any{"enabled": false})
	run("delete-original", nil, map[string]any{"enabled": true})
	cfg, _ := env.configs.Get(ctx, guildID)
	if cfg.PreserveAlpha || cfg.StripMetadata || !cfg.DeleteOriginal {
		t.Fatalf("toggles not applied: %+v", cfg)
	}

	show := env.bot.handleConfig(ctx, guildID, manageGuild, slashData("show", nil, nil))
	if len(show.Embeds) != 1 || show.Embeds[0].Fields[1].Value != "1500 × 1500 px" {
		t.Fatalf("show embed = %+v", show.Embeds)
	}
	reset := env.bot.handleConfig(ctx, guildID, manageGuild, slashData("reset", nil, nil))
	if len(reset.Components) != 1 {
		t.Fatal("reset must ask for confirmation")
	}
	if ok, _ := env.configs.IsChannelAllowed(ctx, guildID, 10); !ok {
		t.Fatal("reset executed without confirmation")
	}
}

func TestCommandDefinitions(t *testing.T) {
	raw, err := json.Marshal(Commands())
	if err != nil {
		t.Fatal(err)
	}
	var cmds []map[string]any
	if err := json.Unmarshal(raw, &cmds); err != nil {
		t.Fatal(err)
	}
	perms := map[string]any{}
	for _, c := range cmds {
		perms[c["name"].(string)] = c["default_member_permissions"]
	}
	if perms["config"] != fmt.Sprint(int64(discord.PermissionManageGuild)) {
		t.Fatalf("/config default_member_permissions = %v", perms["config"])
	}
	if perms["convert"] != nil {
		t.Fatalf("/convert must be usable by everyone, got %v", perms["convert"])
	}
}

// ---- helpers ----

func TestUserMessages(t *testing.T) {
	cases := map[error]string{
		imageproc.ErrUnsupportedFormat: "Unsupported image format.",
		imageproc.ErrDecode:            "Failed to decode the image.",
		imageproc.ErrTooLarge:          "The image is too large to process safely.",
		imageproc.ErrFileSizeLimit:     "Unable to reduce the image below the configured file-size limit.",
		errUpload:                      "Failed to upload the converted image.",
		errUploadTooLarge:              "The converted image is too large to upload to Discord.",
	}
	for err, want := range cases {
		if got := userMessage(fmt.Errorf("ctx: %w", err)); got != want {
			t.Errorf("userMessage(%v) = %q, want %q", err, got, want)
		}
	}
	if strings.Contains(userMessage(errors.New("secret internal detail")), "secret") {
		t.Error("internal error detail leaked to user")
	}
}

func TestDownload(t *testing.T) {
	ctx := context.Background()
	allowAll := func(*url.URL) bool { return true }
	ok := cdnServer(t, []byte("0123456789"))

	if data, err := download(ctx, newDownloadClient(), allowAll, ok.URL, 10); err != nil || string(data) != "0123456789" {
		t.Fatalf("download = %q, %v", data, err)
	}
	if _, err := download(ctx, newDownloadClient(), allowAll, ok.URL, 9); !errors.Is(err, errInputTooLarge) {
		t.Fatalf("oversized body: err = %v", err)
	}
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	if _, err := download(ctx, newDownloadClient(), allowAll, notFound.URL, 10); !errors.Is(err, errDownload) {
		t.Fatalf("404: err = %v", err)
	}
	if _, err := download(ctx, newDownloadClient(), isDiscordCDN, ok.URL, 10); !errors.Is(err, errDownload) {
		t.Fatalf("non-Discord host: err = %v", err)
	}
}

func TestIsDiscordCDN(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://cdn.discordapp.com/attachments/1/2/a.png":   true,
		"https://media.discordapp.net/attachments/1/2/a.png": true,
		"http://cdn.discordapp.com/a.png":                    false,
		"https://cdn.discordapp.com.evil.example/a.png":      false,
		"https://169.254.169.254/latest/meta-data":           false,
	} {
		u, _ := url.Parse(raw)
		if got := isDiscordCDN(u); got != want {
			t.Errorf("isDiscordCDN(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestDisplayName(t *testing.T) {
	if got := displayName("../../etc/`passwd`\n.jpg"); strings.ContainsAny(got, "/`\n") {
		t.Fatalf("displayName = %q", got)
	}
	if got := displayName(""); got != "image" {
		t.Fatalf("displayName(\"\") = %q", got)
	}
}
