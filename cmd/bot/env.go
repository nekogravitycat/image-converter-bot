package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/disgoorg/snowflake/v2"
)

// runtimeConfig is infrastructure configuration from the environment; guild settings live in SQLite.
type runtimeConfig struct {
	Token            string
	DatabasePath     string
	LogLevel         slog.Level
	WorkerCount      int
	WorkQueueSize    int
	MaxInputFileSize int64
	MaxInputPixels   int64
	HealthAddr       string
	DevGuildID       snowflake.ID // optional: register commands to one guild for instant updates
}

func loadRuntimeConfig() (runtimeConfig, error) {
	cfg := runtimeConfig{
		Token:        strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN")),
		DatabasePath: envOr("DATABASE_PATH", "data/bot.db"),
		HealthAddr:   os.Getenv("HEALTH_ADDR"),
	}
	var errs []error
	if cfg.Token == "" {
		errs = append(errs, errors.New("DISCORD_BOT_TOKEN is required"))
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(envOr("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}
	cfg.WorkerCount = int(envInt("WORKER_COUNT", 2, 1, 64, &errs))
	cfg.WorkQueueSize = int(envInt("WORK_QUEUE_SIZE", 32, 1, 10000, &errs))
	cfg.MaxInputFileSize = envInt("MAX_INPUT_FILE_SIZE", 50<<20, 1024, 1<<30, &errs)
	cfg.MaxInputPixels = envInt("MAX_INPUT_PIXELS", 100_000_000, 1, 1<<34, &errs)
	if v := os.Getenv("DISCORD_DEV_GUILD_ID"); v != "" {
		id, err := snowflake.Parse(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("DISCORD_DEV_GUILD_ID: %w", err))
		}
		cfg.DevGuildID = id
	}
	return cfg, errors.Join(errs...)
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def, lo, hi int64, errs *[]error) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < lo || v > hi {
		*errs = append(*errs, fmt.Errorf("%s must be an integer between %d and %d, got %q", key, lo, hi, raw))
		return def
	}
	return v
}
