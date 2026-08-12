package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
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

// rawText reads a column as text, bypassing the driver's TIMESTAMP handling, so
// these tests can assert on what is actually stored rather than on what the
// driver hands back after parsing it.
func rawText(t *testing.T, conn *sql.DB, query string, args ...any) string {
	t.Helper()
	var s string
	if err := conn.QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return s
}

// TestTimestampsStoredAsComparableUTC pins the DSN's _time_format/_timezone
// pair. Without them the driver writes Go's time.Time.String() -- local wall
// clock plus a monotonic clock reading, e.g.
//
//	2026-09-09 09:40:21.365288 +0800 CST m=+2592020.551943710
//
// which shares no format with the CURRENT_TIMESTAMP columns next to it and made
// every SQL comparison between the two a lexicographic accident.
func TestTimestampsStoredAsComparableUTC(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	// A fixed instant in a non-UTC zone: if the write path ignored the zone,
	// the stored text would be the local wall clock instead.
	zone := time.FixedZone("UTC+8", 8*60*60)
	want := time.Date(2026, 9, 9, 1, 40, 21, 0, time.UTC)
	if _, err := conn.Exec(
		`INSERT INTO sessions (token, expires_at, user_id) VALUES ('t', ?, 1)`, want.In(zone),
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	stored := rawText(t, conn, `SELECT CAST(expires_at AS TEXT) FROM sessions WHERE token = 't'`)
	if _, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", stored); err != nil {
		t.Errorf("stored timestamp %q is not ISO-8601: %v", stored, err)
	}
	if want.Format("2006-01-02 15:04:05") != stored[:19] {
		t.Errorf("stored timestamp %q is not the UTC wall clock of %v", stored, want)
	}

	// It round-trips to the same instant through the driver...
	var got time.Time
	if err := conn.QueryRow(`SELECT expires_at FROM sessions WHERE token = 't'`).Scan(&got); err != nil {
		t.Fatalf("scan expires_at: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("round-tripped %v, want %v", got, want)
	}

	// ...and SQLite's own date functions can read it, which is what
	// GetValidSession's unixepoch() comparison relies on.
	if epoch := rawText(t, conn,
		`SELECT CAST(unixepoch(expires_at) AS TEXT) FROM sessions WHERE token = 't'`,
	); epoch != fmt.Sprint(want.Unix()) {
		t.Errorf("unixepoch(expires_at) = %s, want %d", epoch, want.Unix())
	}
}

// applyFirstMigration brings a fresh database to the state it had before
// migration 0002, so the rewrite in that file can be tested against rows in the
// old format rather than against rows it has already fixed.
func applyFirstMigration(t *testing.T, conn *sql.DB) {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	first := entries[0].Name()
	body, err := migrationsFS.ReadFile("migrations/" + first)
	if err != nil {
		t.Fatalf("read %s: %v", first, err)
	}
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := conn.Exec(string(body)); err != nil {
		t.Fatalf("apply %s: %v", first, err)
	}
	if _, err := conn.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, first); err != nil {
		t.Fatalf("record %s: %v", first, err)
	}
}

// TestMigrationRewritesLegacyTimestamps exercises migration 0002's rewrite of
// the timestamps an earlier build stored in Go's own format, on a database that
// really is in the pre-0002 state.
func TestMigrationRewritesLegacyTimestamps(t *testing.T) {
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "app.db")+
		"?_pragma=busy_timeout(5000)&_time_format=sqlite&_timezone=UTC")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	applyFirstMigration(t, conn)

	cases := []struct {
		name   string
		legacy string
		want   string // expected stored value after the migration
	}{
		{
			name:   "positive offset with fraction",
			legacy: "2026-08-11 09:40:58.357245 +0800 CST m=+86457.543907751",
			want:   "2026-08-11 01:40:58+00:00",
		},
		{
			// The ' -' discriminator has to survive the hyphens in the date.
			name:   "negative offset without fraction",
			legacy: "2026-08-11 09:40:58 -0500 EST m=+1.500000001",
			want:   "2026-08-11 14:40:58+00:00",
		},
		{
			name:   "already UTC in Go's format",
			legacy: "2026-08-11 09:40:58.1 +0000 UTC m=+2.5",
			want:   "2026-08-11 09:40:58+00:00",
		},
		{
			// Anything already in the new format must be left exactly alone.
			name:   "already migrated",
			legacy: "2026-08-11 09:40:58+00:00",
			want:   "2026-08-11 09:40:58+00:00",
		},
	}

	for i, c := range cases {
		slug := fmt.Sprintf("s%d", i)
		if _, err := conn.Exec(
			`INSERT INTO files (slug, name, html_content, access_code, user_id, expires_at)
			 VALUES (?, 'n', '<p>h</p>', 'code', 1, ?)`, slug, c.legacy,
		); err != nil {
			t.Fatalf("insert file %s: %v", slug, err)
		}
		if _, err := conn.Exec(
			`INSERT INTO sessions (token, expires_at, user_id) VALUES (?, ?, 1)`, slug, c.legacy,
		); err != nil {
			t.Fatalf("insert session %s: %v", slug, err)
		}
	}
	// A NULL expiry (the common case) must stay NULL rather than becoming a date.
	if _, err := conn.Exec(
		`INSERT INTO files (slug, name, html_content, access_code, user_id)
		 VALUES ('nolimit', 'n', '<p>h</p>', 'code', 1)`,
	); err != nil {
		t.Fatalf("insert file without expiry: %v", err)
	}

	if err := migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for i, c := range cases {
		slug := fmt.Sprintf("s%d", i)
		t.Run(c.name, func(t *testing.T) {
			if got := rawText(t, conn,
				`SELECT CAST(expires_at AS TEXT) FROM files WHERE slug = ?`, slug,
			); got != c.want {
				t.Errorf("files.expires_at = %q, want %q", got, c.want)
			}
			if got := rawText(t, conn,
				`SELECT CAST(expires_at AS TEXT) FROM sessions WHERE token = ?`, slug,
			); got != c.want {
				t.Errorf("sessions.expires_at = %q, want %q", got, c.want)
			}
		})
	}

	var expires sql.NullTime
	if err := conn.QueryRow(`SELECT expires_at FROM files WHERE slug = 'nolimit'`).Scan(&expires); err != nil {
		t.Fatalf("scan null expiry: %v", err)
	}
	if expires.Valid {
		t.Errorf("a NULL expiry became %v", expires.Time)
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

// applyMigrationsBefore applies every migration up to but excluding the named
// one, leaving a database in exactly the state the previous release left it.
func applyMigrationsBefore(t *testing.T, conn *sql.DB, exclude string) {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name >= exclude {
			break
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := conn.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := conn.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name); err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
	}
}

// TestMigrationBackfillsContentSize covers the upgrade path for migration 0003.
//
// content_size is what lets the listings stop reading html_content, and the
// quota check trust a SUM instead of measuring every document. Existing rows
// predate the column, so if the backfill were wrong every file already in the
// database would report zero bytes: the dashboard would show "0 B" for real
// files and the quota would consider a full account empty.
func TestMigrationBackfillsContentSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_time_format=sqlite&_timezone=UTC"

	legacy, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	legacy.SetMaxOpenConns(1)
	applyMigrationsBefore(t, legacy, "0003")

	rows := []struct {
		slug, content string
		wantBytes     int64
	}{
		{"ascii", "<p>hello</p>", 12},
		// Bytes, not characters: length() on TEXT would report 4 here and
		// undercount every non-ASCII document in the database.
		{"cjk", "汉字测试", 12},
		{"empty-ish", "x", 1},
	}
	for _, r := range rows {
		if _, err := legacy.Exec(
			`INSERT INTO files (slug, name, html_content, access_code, user_id)
			 VALUES (?, 'n', ?, 'code', 1)`, r.slug, r.content,
		); err != nil {
			t.Fatalf("insert %s: %v", r.slug, err)
		}
	}
	if _, err := legacy.Exec(
		`INSERT INTO users (username, nickname, password_hash) VALUES ('a', 'A', 'h')`,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	legacy.Close()

	// Reopening is the upgrade: db.Open runs whatever has not been recorded.
	conn, err := Open(path)
	if err != nil {
		t.Fatalf("Open (upgrade): %v", err)
	}
	defer conn.Close()

	for _, r := range rows {
		var got int64
		if err := conn.QueryRow(
			`SELECT content_size FROM files WHERE slug = ?`, r.slug).Scan(&got); err != nil {
			t.Fatalf("read content_size for %s: %v", r.slug, err)
		}
		if got != r.wantBytes {
			t.Errorf("content_size(%s) = %d, want %d", r.slug, got, r.wantBytes)
		}
	}

	// The pre-existing account picks up the default quota rather than 0, which
	// would silently block every upload after an upgrade.
	var quota int64
	if err := conn.QueryRow(`SELECT quota_bytes FROM users WHERE username = 'a'`).Scan(&quota); err != nil {
		t.Fatalf("read quota_bytes: %v", err)
	}
	if quota != 100<<20 {
		t.Errorf("quota_bytes = %d after upgrade, want the 100MB default", quota)
	}
}

// TestContentSizeIsSelfCorrecting pins the triggers that keep files.content_size
// honest regardless of which binary wrote the row.
//
// The realistic way to get a wrong value is a downgrade: an older image runs
// happily against an upgraded database (sqlc expands SELECT * into explicit
// column names at generation time, so columns added later are simply ignored),
// but its INSERT does not name content_size and takes the DEFAULT 0. Rolling
// forward again would not repair those rows, because migration 0003 is already
// recorded and its backfill never runs twice -- leaving files that report zero
// bytes and count nothing against their owner's quota.
func TestContentSizeIsSelfCorrecting(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	// Exactly what an older binary's INSERT looks like: no content_size.
	const content = "汉字内容" // 12 bytes, 4 characters
	if _, err := conn.Exec(
		`INSERT INTO files (slug, name, html_content, access_code, user_id)
		 VALUES ('legacy', 'n', ?, 'code', 1)`, content,
	); err != nil {
		t.Fatalf("insert without content_size: %v", err)
	}

	var size int64
	if err := conn.QueryRow(`SELECT content_size FROM files WHERE slug = 'legacy'`).Scan(&size); err != nil {
		t.Fatalf("read content_size: %v", err)
	}
	if size != int64(len(content)) {
		t.Errorf("content_size = %d after an insert that omitted it, want %d bytes", size, len(content))
	}

	// The same for an update that rewrites the content without the size.
	const longer = "a much longer body than before"
	if _, err := conn.Exec(
		`UPDATE files SET html_content = ? WHERE slug = 'legacy'`, longer,
	); err != nil {
		t.Fatalf("update without content_size: %v", err)
	}
	if err := conn.QueryRow(`SELECT content_size FROM files WHERE slug = 'legacy'`).Scan(&size); err != nil {
		t.Fatalf("read content_size: %v", err)
	}
	if size != int64(len(longer)) {
		t.Errorf("content_size = %d after an update that omitted it, want %d", size, len(longer))
	}

	// A counter bump must not be treated as a content change -- /res/{slug}
	// issues one on every request, and AFTER UPDATE OF html_content is what
	// keeps those off the trigger.
	if _, err := conn.Exec(
		`UPDATE files SET success_count = success_count + 1 WHERE slug = 'legacy'`,
	); err != nil {
		t.Fatalf("bump counter: %v", err)
	}
	var updatedAt, bumped string
	if err := conn.QueryRow(
		`SELECT CAST(updated_at AS TEXT), CAST(success_count AS TEXT) FROM files WHERE slug = 'legacy'`,
	).Scan(&updatedAt, &bumped); err != nil {
		t.Fatalf("read after bump: %v", err)
	}
	if bumped != "1" {
		t.Errorf("success_count = %s, want 1", bumped)
	}
}
