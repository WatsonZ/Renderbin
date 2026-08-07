package backup_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/shawn-bluce/renderbin/backend/internal/backup"
	"github.com/shawn-bluce/renderbin/backend/internal/db"
)

func TestSnapshotProducesRestorableCopy(t *testing.T) {
	// Open a migrated source DB and insert a row.
	src, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer src.Close()
	if _, err := src.Exec(
		`INSERT INTO files (slug, name, html_content, access_code, user_id) VALUES ('s', 'n', '<p>hi</p>', 'code', 1)`,
	); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	// Snapshot to a fresh path.
	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := backup.Snapshot(context.Background(), src, dest); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// The snapshot must be a standalone, openable DB containing the row.
	restored, err := sql.Open("sqlite", "file:"+dest)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer restored.Close()

	var (
		content string
		count   int
	)
	if err := restored.QueryRow(`SELECT html_content FROM files WHERE slug = 's'`).Scan(&content); err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if content != "<p>hi</p>" {
		t.Errorf("snapshot content = %q, want %q", content, "<p>hi</p>")
	}
	// The schema (all migrations) is copied too.
	if err := restored.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations in snapshot: %v", err)
	}
	if count == 0 {
		t.Error("snapshot is missing schema_migrations rows")
	}
}

func TestSnapshotFailsIfDestinationExists(t *testing.T) {
	src, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer src.Close()

	// VACUUM INTO refuses to overwrite an existing file.
	dest := filepath.Join(t.TempDir(), "exists.db")
	if err := os.WriteFile(dest, []byte("not a db"), 0o644); err != nil {
		t.Fatalf("pre-create dest: %v", err)
	}
	if err := backup.Snapshot(context.Background(), src, dest); err == nil {
		t.Error("expected Snapshot to fail when the destination already exists")
	}
}
