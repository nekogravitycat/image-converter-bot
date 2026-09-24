// Command bot runs the Discord VRChat image converter.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/disgoorg/disgo"
	disgobot "github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"

	"github.com/nekogravitycat/image-converter-bot/internal/bot"
	"github.com/nekogravitycat/image-converter-bot/internal/config"
	"github.com/nekogravitycat/image-converter-bot/internal/database"
	"github.com/nekogravitycat/image-converter-bot/internal/imageproc"
	"github.com/nekogravitycat/image-converter-bot/internal/worker"
)

const shutdownGrace = 20 * time.Second

func main() {
	selfcheck := flag.Bool("selfcheck", false, "verify libvips capabilities (including HEIC decode) and exit")
	healthcheck := flag.Bool("healthcheck", false, "query the local health endpoint and exit (for Docker HEALTHCHECK)")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if *selfcheck {
		if err := checkImaging(logger); err != nil {
			logger.Error("selfcheck failed", slog.Any("err", err))
			os.Exit(1)
		}
		logger.Info("selfcheck passed", slog.String("libvips", imageproc.VipsVersion()))
		return
	}

	if err := run(); err != nil {
		logger.Error("fatal", slog.Any("err", err))
		os.Exit(1)
	}
}

func checkImaging(logger *slog.Logger) error {
	if err := imageproc.Startup(logger); err != nil {
		return err
	}
	return imageproc.CheckCapabilities()
}

func run() error {
	rc, err := loadRuntimeConfig()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: rc.LogLevel}))
	slog.SetDefault(logger)
	logger.Info("starting", slog.String("disgo", disgo.Version), slog.Int("workers", rc.WorkerCount), slog.Int("queue_size", rc.WorkQueueSize))

	// HEIC is a core requirement, so a missing decoder aborts startup rather than degrading silently.
	if err := checkImaging(logger); err != nil {
		return fmt.Errorf("image capability check: %w", err)
	}
	defer imageproc.Shutdown()
	logger.Info("libvips ready", slog.String("version", imageproc.VipsVersion()))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Migrations must succeed before we talk to Discord.
	db, err := database.Open(ctx, rc.DatabasePath)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			logger.Error("close database", slog.Any("err", err))
		}
	}()

	configs := config.NewService(db)
	pool := worker.New(rc.WorkerCount, rc.WorkQueueSize, logger)
	settings := bot.Settings{
		MaxInputFileSize: rc.MaxInputFileSize,
		ProcessTimeout:   30 * time.Second,
		JobTimeout:       2 * time.Minute,
		APITimeout:       15 * time.Second,
	}

	var b *bot.Bot
	client, err := disgo.New(rc.Token,
		disgobot.WithLogger(logger),
		disgobot.WithGatewayConfigOpts(gateway.WithIntents(
			gateway.IntentGuilds,
			gateway.IntentGuildMessages,
			gateway.IntentMessageContent, // privileged: needed to see attachments on others' messages
		)),
		disgobot.WithEventListenerFunc(func(e *events.GuildMessageCreate) { b.OnGuildMessageCreate(e) }),
		disgobot.WithEventListenerFunc(func(e *events.ApplicationCommandInteractionCreate) { b.OnApplicationCommand(e) }),
		disgobot.WithEventListenerFunc(func(e *events.ComponentInteractionCreate) { b.OnComponent(e) }),
	)
	if err != nil {
		return fmt.Errorf("create discord client: %w", err)
	}
	b = bot.New(client.Rest, configs, pool, imageproc.NewProcessor(rc.MaxInputPixels), settings, logger)

	if err := syncCommands(client, rc); err != nil {
		return err
	}

	if rc.HealthAddr != "" {
		srv := startHealthServer(rc.HealthAddr, client, logger)
		defer func() { _ = srv.Close() }()
	}

	if err := client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("open gateway: %w", err)
	}
	logger.Info("bot is running")

	<-ctx.Done()
	logger.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	// Stop receiving events first, let in-flight jobs finish (they still need REST), then close the rest.
	client.Gateway.Close(sctx)
	if err := pool.Shutdown(sctx); err != nil {
		logger.Warn("jobs did not finish before shutdown deadline", slog.Any("err", err))
	}
	client.Close(sctx)
	logger.Info("shutdown complete")
	return nil
}

func syncCommands(client *disgobot.Client, rc runtimeConfig) error {
	cmds := bot.Commands()
	if rc.DevGuildID != 0 {
		if _, err := client.Rest.SetGuildCommands(client.ApplicationID, rc.DevGuildID, cmds); err != nil {
			return fmt.Errorf("register guild commands: %w", err)
		}
		slog.Info("registered guild commands", slog.String("guild_id", rc.DevGuildID.String()))
		return nil
	}
	if _, err := client.Rest.SetGlobalCommands(client.ApplicationID, cmds); err != nil {
		return fmt.Errorf("register global commands: %w", err)
	}
	slog.Info("registered global commands")
	return nil
}

func startHealthServer(addr string, client *disgobot.Client, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if client.Gateway == nil || client.Gateway.Status() != gateway.StatusReady {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("health server", slog.Any("err", err))
		}
	}()
	return srv
}

func runHealthcheck() int {
	addr := os.Getenv("HEALTH_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "unhealthy:", resp.Status)
		return 1
	}
	return 0
}
