package config_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/disgoorg/snowflake/v2"

	"github.com/nekogravitycat/image-converter-bot/internal/config"
	"github.com/nekogravitycat/image-converter-bot/internal/database"
)

func openService(t *testing.T, path string) *config.Service {
	t.Helper()
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return config.NewService(db)
}

func TestGetCreatesDefaults(t *testing.T) {
	svc := openService(t, filepath.Join(t.TempDir(), "bot.db"))
	cfg, err := svc.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	want := config.Default(1)
	if cfg.MaxWidth != want.MaxWidth || cfg.MaxHeight != want.MaxHeight || cfg.JPEGQuality != 90 ||
		!cfg.PreserveAlpha || !cfg.StripMetadata || cfg.MaxFileSizeBytes != nil || len(cfg.Channels) != 0 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestPersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bot.db")
	guild := snowflake.ID(1234567890123456789)

	svc := openService(t, path)
	if _, err := svc.Update(ctx, guild, func(c *config.Config) {
		c.MaxWidth, c.MaxHeight = 2048, 2048
		size := config.MebibytesToBytes(4)
		c.MaxFileSizeBytes = &size
		c.PreserveAlpha = false
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddChannel(ctx, guild, 42); err != nil {
		t.Fatal(err)
	}

	// A fresh service has an empty cache, so this reads SQLite.
	cfg, err := openService(t, path).Get(ctx, guild)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxWidth != 2048 || cfg.MaxHeight != 2048 || cfg.PreserveAlpha ||
		cfg.MaxFileSizeBytes == nil || *cfg.MaxFileSizeBytes != 4<<20 || !cfg.HasChannel(42) {
		t.Fatalf("config not persisted: %+v", cfg)
	}
}

func TestGuildsAreIndependent(t *testing.T) {
	ctx := context.Background()
	svc := openService(t, filepath.Join(t.TempDir(), "bot.db"))
	if _, err := svc.Update(ctx, 1, func(c *config.Config) { c.MaxWidth = 2048 }); err != nil {
		t.Fatal(err)
	}
	a, _ := svc.Get(ctx, 1)
	b, _ := svc.Get(ctx, 2)
	if a.MaxWidth != 2048 || b.MaxWidth != config.DefaultMaxWidth {
		t.Fatalf("guild configs leaked: a=%d b=%d", a.MaxWidth, b.MaxWidth)
	}
}

func TestChannels(t *testing.T) {
	ctx := context.Background()
	svc := openService(t, filepath.Join(t.TempDir(), "bot.db"))

	if added, err := svc.AddChannel(ctx, 1, 10); err != nil || !added {
		t.Fatalf("first add: added=%v err=%v", added, err)
	}
	if added, err := svc.AddChannel(ctx, 1, 10); err != nil || added {
		t.Fatalf("duplicate add: added=%v err=%v", added, err)
	}
	if ok, _ := svc.IsChannelAllowed(ctx, 1, 10); !ok {
		t.Fatal("channel should be allowed immediately after add")
	}
	if ok, _ := svc.IsChannelAllowed(ctx, 2, 10); ok {
		t.Fatal("channel allowlist leaked across guilds")
	}
	if removed, err := svc.RemoveChannel(ctx, 1, 10); err != nil || !removed {
		t.Fatalf("remove: removed=%v err=%v", removed, err)
	}
	if ok, _ := svc.IsChannelAllowed(ctx, 1, 10); ok {
		t.Fatal("channel should be disallowed immediately after remove")
	}
	if removed, _ := svc.RemoveChannel(ctx, 1, 10); removed {
		t.Fatal("second remove should report not removed")
	}
}

func TestResetRestoresDefaultsAndClearsChannels(t *testing.T) {
	ctx := context.Background()
	svc := openService(t, filepath.Join(t.TempDir(), "bot.db"))
	if _, err := svc.Update(ctx, 1, func(c *config.Config) { c.JPEGQuality = 50 }); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddChannel(ctx, 1, 10); err != nil {
		t.Fatal(err)
	}
	cfg, err := svc.Reset(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JPEGQuality != config.DefaultJPEGQuality || len(cfg.Channels) != 0 {
		t.Fatalf("reset did not restore defaults: %+v", cfg)
	}
}

func TestUpdateRejectsInvalid(t *testing.T) {
	ctx := context.Background()
	svc := openService(t, filepath.Join(t.TempDir(), "bot.db"))
	cases := map[string]func(*config.Config){
		"zero width":     func(c *config.Config) { c.MaxWidth = 0 },
		"huge height":    func(c *config.Config) { c.MaxHeight = config.MaxDimension + 1 },
		"quality 0":      func(c *config.Config) { c.JPEGQuality = 0 },
		"quality 101":    func(c *config.Config) { c.JPEGQuality = 101 },
		"tiny file size": func(c *config.Config) { v := int64(10); c.MaxFileSizeBytes = &v },
	}
	for name, mutate := range cases {
		if _, err := svc.Update(ctx, 1, mutate); !errors.Is(err, config.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	cfg, _ := svc.Get(ctx, 1)
	if cfg.MaxWidth != config.DefaultMaxWidth || cfg.JPEGQuality != config.DefaultJPEGQuality {
		t.Fatalf("invalid update leaked into config: %+v", cfg)
	}
}

func TestGetReturnsCopy(t *testing.T) {
	ctx := context.Background()
	svc := openService(t, filepath.Join(t.TempDir(), "bot.db"))
	if _, err := svc.AddChannel(ctx, 1, 10); err != nil {
		t.Fatal(err)
	}
	cfg, _ := svc.Get(ctx, 1)
	cfg.Channels[0] = 99
	again, _ := svc.Get(ctx, 1)
	if again.Channels[0] != 10 {
		t.Fatal("mutating a returned config changed the cache")
	}
}
