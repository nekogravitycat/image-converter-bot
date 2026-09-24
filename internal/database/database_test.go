package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenMigratesIdempotently(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sub", "bot.db")

	for range 2 {
		db, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		v, err := SchemaVersion(ctx, db)
		if err != nil {
			t.Fatalf("SchemaVersion: %v", err)
		}
		if v != len(migrations) {
			t.Fatalf("schema version = %d, want %d", v, len(migrations))
		}
		var fk int
		if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
			t.Fatalf("foreign_keys = %d (err %v), want 1", fk, err)
		}
		db.Close()
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bot.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (999, 0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if _, err := Open(ctx, path); err == nil {
		t.Fatal("expected error for newer schema version")
	}
}
