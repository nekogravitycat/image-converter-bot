package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// migrations are applied in order; the index+1 is the schema version.
// Never edit an existing entry, only append new ones.
var migrations = []string{
	// 1: guild configuration and channel allowlist.
	`CREATE TABLE IF NOT EXISTS guild_configs (
		guild_id            TEXT PRIMARY KEY,
		max_width           INTEGER NOT NULL,
		max_height          INTEGER NOT NULL,
		max_file_size_bytes INTEGER NULL,
		jpeg_quality        INTEGER NOT NULL,
		preserve_alpha      INTEGER NOT NULL,
		strip_metadata      INTEGER NOT NULL,
		created_at          INTEGER NOT NULL,
		updated_at          INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS allowed_channels (
		guild_id   TEXT NOT NULL,
		channel_id TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (guild_id, channel_id),
		FOREIGN KEY (guild_id) REFERENCES guild_configs(guild_id) ON DELETE CASCADE
	);`,
	// 2: option to delete the user's original message once its attachments are converted.
	`ALTER TABLE guild_configs ADD COLUMN delete_original INTEGER NOT NULL DEFAULT 0;`,
}

// Migrate brings the schema up to the latest version. It is safe to run repeatedly.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var current int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this build supports (%d)", current, len(migrations))
	}

	for i := current; i < len(migrations); i++ {
		version := i + 1
		if err := applyMigration(ctx, db, version, migrations[i]); err != nil {
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, version int, stmt string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		version, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// SchemaVersion reports the currently applied schema version.
func SchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}
