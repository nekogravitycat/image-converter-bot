package config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// Service reads and writes guild configuration. SQLite is the source of truth;
// the cache is refreshed under the same lock as every write, so updates apply immediately.
type Service struct {
	db  *sql.DB
	now func() time.Time

	mu    sync.RWMutex
	cache map[snowflake.ID]Config
}

// NewService returns a Service backed by db with an empty cache.
func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now, cache: make(map[snowflake.ID]Config)}
}

// Get returns the guild's configuration, creating the default row on first use.
func (s *Service) Get(ctx context.Context, guildID snowflake.ID) (Config, error) {
	s.mu.RLock()
	cfg, ok := s.cache[guildID]
	s.mu.RUnlock()
	if ok {
		return cfg.Clone(), nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg, ok := s.cache[guildID]; ok {
		return cfg.Clone(), nil
	}
	cfg, err := s.loadOrCreateLocked(ctx, guildID)
	if err != nil {
		return Config{}, err
	}
	return cfg.Clone(), nil
}

// IsChannelAllowed reports whether auto conversion is enabled for channelID.
func (s *Service) IsChannelAllowed(ctx context.Context, guildID, channelID snowflake.ID) (bool, error) {
	cfg, err := s.Get(ctx, guildID)
	if err != nil {
		return false, err
	}
	return cfg.HasChannel(channelID), nil
}

// Update applies mutate to the guild's settings (not its channels), validates and persists the result.
func (s *Service) Update(ctx context.Context, guildID snowflake.ID, mutate func(*Config)) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadOrCreateLocked(ctx, guildID)
	if err != nil {
		return Config{}, err
	}
	cfg = cfg.Clone()
	mutate(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	var maxSize any
	if cfg.MaxFileSizeBytes != nil {
		maxSize = *cfg.MaxFileSizeBytes
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE guild_configs SET
			max_width = ?, max_height = ?, max_file_size_bytes = ?, jpeg_quality = ?,
			preserve_alpha = ?, strip_metadata = ?, updated_at = ?
		WHERE guild_id = ?`,
		cfg.MaxWidth, cfg.MaxHeight, maxSize, cfg.JPEGQuality,
		boolToInt(cfg.PreserveAlpha), boolToInt(cfg.StripMetadata), s.now().Unix(),
		guildKey(guildID)); err != nil {
		return Config{}, fmt.Errorf("update guild config: %w", err)
	}
	return s.reloadLocked(ctx, guildID)
}

// AddChannel enables auto conversion in channelID. added is false if it was already enabled.
func (s *Service) AddChannel(ctx context.Context, guildID, channelID snowflake.ID) (added bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.loadOrCreateLocked(ctx, guildID); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO allowed_channels (guild_id, channel_id, created_at) VALUES (?, ?, ?)`,
		guildKey(guildID), channelID.String(), s.now().Unix())
	if err != nil {
		return false, fmt.Errorf("insert allowed channel: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert allowed channel: %w", err)
	}
	if _, err := s.reloadLocked(ctx, guildID); err != nil {
		return false, err
	}
	return n > 0, nil
}

// RemoveChannel disables auto conversion in channelID. removed is false if it was not enabled.
func (s *Service) RemoveChannel(ctx context.Context, guildID, channelID snowflake.ID) (removed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.ExecContext(ctx, `DELETE FROM allowed_channels WHERE guild_id = ? AND channel_id = ?`,
		guildKey(guildID), channelID.String())
	if err != nil {
		return false, fmt.Errorf("delete allowed channel: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete allowed channel: %w", err)
	}
	if _, err := s.reloadLocked(ctx, guildID); err != nil {
		return false, err
	}
	return n > 0, nil
}

// Reset restores the default settings and clears the channel allowlist.
func (s *Service) Reset(ctx context.Context, guildID snowflake.ID) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Config{}, fmt.Errorf("reset guild config: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	// Deleting the row cascades to allowed_channels.
	if _, err := tx.ExecContext(ctx, `DELETE FROM guild_configs WHERE guild_id = ?`, guildKey(guildID)); err != nil {
		return Config{}, fmt.Errorf("reset guild config: %w", err)
	}
	if err := insertDefault(ctx, tx, guildID, s.now()); err != nil {
		return Config{}, err
	}
	if err := tx.Commit(); err != nil {
		return Config{}, fmt.Errorf("reset guild config: %w", err)
	}
	return s.reloadLocked(ctx, guildID)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertDefault(ctx context.Context, db execer, guildID snowflake.ID, now time.Time) error {
	d := Default(guildID)
	if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO guild_configs
			(guild_id, max_width, max_height, max_file_size_bytes, jpeg_quality, preserve_alpha, strip_metadata, created_at, updated_at)
		VALUES (?, ?, ?, NULL, ?, ?, ?, ?, ?)`,
		guildKey(guildID), d.MaxWidth, d.MaxHeight, d.JPEGQuality,
		boolToInt(d.PreserveAlpha), boolToInt(d.StripMetadata), now.Unix(), now.Unix()); err != nil {
		return fmt.Errorf("insert default guild config: %w", err)
	}
	return nil
}

// loadOrCreateLocked returns the cached config or loads it, inserting defaults if missing. Caller holds s.mu.
func (s *Service) loadOrCreateLocked(ctx context.Context, guildID snowflake.ID) (Config, error) {
	if cfg, ok := s.cache[guildID]; ok {
		return cfg, nil
	}
	if err := insertDefault(ctx, s.db, guildID, s.now()); err != nil {
		return Config{}, err
	}
	return s.reloadLocked(ctx, guildID)
}

// reloadLocked reads the guild from SQLite and replaces the cache entry. Caller holds s.mu.
func (s *Service) reloadLocked(ctx context.Context, guildID snowflake.ID) (Config, error) {
	cfg := Config{GuildID: guildID}
	var (
		maxSize                      sql.NullInt64
		preserveAlpha, stripMetadata int
	)
	err := s.db.QueryRowContext(ctx, `SELECT max_width, max_height, max_file_size_bytes, jpeg_quality, preserve_alpha, strip_metadata
		FROM guild_configs WHERE guild_id = ?`, guildKey(guildID)).
		Scan(&cfg.MaxWidth, &cfg.MaxHeight, &maxSize, &cfg.JPEGQuality, &preserveAlpha, &stripMetadata)
	if errors.Is(err, sql.ErrNoRows) {
		delete(s.cache, guildID)
		return Config{}, fmt.Errorf("guild config %s not found", guildID)
	}
	if err != nil {
		return Config{}, fmt.Errorf("load guild config: %w", err)
	}
	if maxSize.Valid {
		v := maxSize.Int64
		cfg.MaxFileSizeBytes = &v
	}
	cfg.PreserveAlpha = preserveAlpha != 0
	cfg.StripMetadata = stripMetadata != 0

	rows, err := s.db.QueryContext(ctx, `SELECT channel_id FROM allowed_channels WHERE guild_id = ?`, guildKey(guildID))
	if err != nil {
		return Config{}, fmt.Errorf("load allowed channels: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return Config{}, fmt.Errorf("load allowed channels: %w", err)
		}
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("load allowed channels: parse %q: %w", raw, err)
		}
		cfg.Channels = append(cfg.Channels, snowflake.ID(id))
	}
	if err := rows.Err(); err != nil {
		return Config{}, fmt.Errorf("load allowed channels: %w", err)
	}
	slices.Sort(cfg.Channels)

	s.cache[guildID] = cfg
	return cfg, nil
}

func guildKey(id snowflake.ID) string { return id.String() }

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
