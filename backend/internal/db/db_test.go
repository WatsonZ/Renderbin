package db

import (
	"path/filepath"
	"testing"
)

func TestOpenRunsAllMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	// schema_migrations should record exactly one row per embedded migration file.
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	want := len(entries)
	if want == 0 {
		t.Fatal("no migration files embedded")
	}

	var got int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&got); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if got != want {
		t.Errorf("applied migrations = %d, want %d", got, want)
	}

	// Every column the schema declares must exist and be queryable.
	if _, err := conn.Exec(
		`INSERT INTO files (slug, name, html_content, access_code, user_id) VALUES ('s', 'n', '<p>h</p>', 'code', 1)`,
	); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	var (
		successCount, viewCount int64
		tags                    string
	)
	if err := conn.QueryRow(
		`SELECT success_count, view_count, tags FROM files WHERE slug = 's'`,
	).Scan(&successCount, &viewCount, &tags); err != nil {
		t.Fatalf("select added columns: %v", err)
	}

	// sessions must exist too, and also demands an owner.
	if _, err := conn.Exec(
		`INSERT INTO sessions (token, expires_at, user_id) VALUES ('t', CURRENT_TIMESTAMP, 1)`,
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")

	conn, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	var first int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&first); err != nil {
		t.Fatalf("count after first open: %v", err)
	}
	conn.Close()

	// Re-opening the same file must not re-apply migrations or error.
	conn2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer conn2.Close()
	var second int
	if err := conn2.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&second); err != nil {
		t.Fatalf("count after second open: %v", err)
	}
	if first != second {
		t.Errorf("migration count changed on reopen: first=%d second=%d", first, second)
	}
}

func TestOpenEnablesForeignKeys(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	var fk int
	if err := conn.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("pragma foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}
