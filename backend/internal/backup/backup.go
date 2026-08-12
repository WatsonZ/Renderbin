// Package backup produces consistent, restorable snapshots of the live SQLite
// database, and restores one back over it.
//
// Snapshot uses SQLite's VACUUM INTO, which writes a fresh standalone database
// file (no WAL sidecar, fully checkpointed) — safe to run against the live
// connection without stopping the server.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/shawn-bluce/renderbin/backend/internal/db"
)

// Snapshot writes a consistent copy of database to destPath using VACUUM INTO.
// destPath must not already exist (SQLite refuses to overwrite it).
func Snapshot(ctx context.Context, database *sql.DB, destPath string) error {
	// VACUUM INTO takes a string literal, not a bound parameter, so the path
	// is interpolated. destPath is server-controlled (a temp path), never user
	// input; single quotes are still escaped defensively.
	escaped := strings.ReplaceAll(destPath, "'", "''")
	if _, err := database.ExecContext(ctx, "VACUUM INTO '"+escaped+"'"); err != nil {
		return fmt.Errorf("vacuum into %s: %w", destPath, err)
	}
	return nil
}

// Rejections that are the uploader's fault rather than the server's, so callers
// can answer 400 instead of 500.
var (
	ErrNotSQLite      = errors.New("the uploaded file is not a SQLite database")
	ErrNoAccounts     = errors.New("the snapshot contains no accounts, so restoring it would lock everyone out")
	ErrSchemaMismatch = errors.New("the snapshot's schema does not match this version of the app")
)

// sqliteMagic is the 16-byte header every SQLite database file starts with.
const sqliteMagic = "SQLite format 3\x00"

// Stats reports what a restored snapshot contained.
type Stats struct {
	Users int64
	Files int64
}

// Restore replaces the contents of the live database with those of the snapshot
// at path.
//
// It copies row data into the *existing* database rather than swapping the file
// underneath the process. Swapping would mean closing the live *sql.DB and
// handing a new one to every handler that captured a *sqlcgen.Queries at
// construction — a wide change with a window where the database is simply gone.
// Copying instead means the connection every handler already holds sees the
// restored rows on its next query: no reopen, no restart, nothing to coordinate.
//
// The copy runs as one transaction on one physical connection, so a failure
// anywhere leaves the live data exactly as it was. The snapshot is first opened
// through db.Open, which migrates it up to the current schema — that is what
// makes a backup taken by an older build restorable by a newer one.
//
// Faithfulness is the point: the sessions table is replaced too, so whoever
// triggered the restore is signed out unless their session happens to be in the
// snapshot. Callers should expect to send the user back through login.
func Restore(ctx context.Context, live *sql.DB, path string) (Stats, error) {
	if err := checkSQLiteHeader(path); err != nil {
		return Stats{}, err
	}

	// Opening through db.Open validates the file as a database of ours and
	// brings its schema up to date. It writes to the file (migrations, WAL),
	// which is fine — callers hand us a throwaway copy of the upload.
	snapshot, err := db.Open(path)
	if err != nil {
		return Stats{}, fmt.Errorf("%w: %v", ErrNotSQLite, err)
	}
	stats, err := snapshotStats(ctx, snapshot)
	if err != nil {
		snapshot.Close()
		return Stats{}, err
	}
	// Close before attaching, so the snapshot's WAL is checkpointed into the
	// file the other connection is about to read.
	if err := snapshot.Close(); err != nil {
		return Stats{}, fmt.Errorf("close snapshot: %w", err)
	}

	if err := copyInto(ctx, live, path); err != nil {
		return Stats{}, err
	}
	return stats, nil
}

func checkSQLiteHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open snapshot: %w", err)
	}
	defer f.Close()

	header := make([]byte, len(sqliteMagic))
	if _, err := f.Read(header); err != nil || string(header) != sqliteMagic {
		return ErrNotSQLite
	}
	return nil
}

// snapshotStats doubles as the last validity gate: a database with no users is
// either not one of ours or an empty shell, and restoring it would leave an
// instance nobody can sign in to with the previous data already gone.
func snapshotStats(ctx context.Context, snapshot *sql.DB) (Stats, error) {
	var stats Stats
	if err := snapshot.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&stats.Users); err != nil {
		return Stats{}, fmt.Errorf("%w: %v", ErrNotSQLite, err)
	}
	if stats.Users == 0 {
		return Stats{}, ErrNoAccounts
	}
	if err := snapshot.QueryRowContext(ctx, `SELECT COUNT(*) FROM files`).Scan(&stats.Files); err != nil {
		return Stats{}, fmt.Errorf("%w: %v", ErrNotSQLite, err)
	}
	return stats, nil
}

// copyInto attaches the snapshot and replaces every table's contents with its
// rows, inside a single transaction.
func copyInto(ctx context.Context, live *sql.DB, path string) error {
	// One physical connection for ATTACH, the transaction and DETACH: an
	// attached schema is per-connection state, and the pool could otherwise
	// hand the transaction a different connection than the one holding it.
	conn, err := live.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Close()

	escaped := strings.ReplaceAll(path, "'", "''")
	if _, err := conn.ExecContext(ctx, "ATTACH DATABASE '"+escaped+"' AS restore_src"); err != nil {
		return fmt.Errorf("attach snapshot: %w", err)
	}
	// Detach even when the copy fails, or a later restore on this connection
	// collides with the leftover attachment. WithoutCancel so an aborted
	// request still cleans up after itself.
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "DETACH DATABASE restore_src")
	}()

	tables, err := restorableTables(ctx, conn)
	if err != nil {
		return err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin restore: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, table := range tables {
		cols, err := copyColumns(ctx, tx, table)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM main.`+quoteIdent(table)); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO main.`+quoteIdent(table)+` (`+cols+`) SELECT `+cols+` FROM restore_src.`+quoteIdent(table),
		); err != nil {
			return fmt.Errorf("copy %s: %w", table, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit restore: %w", err)
	}
	return nil
}

// restorableTables lists what to copy: every table of ours, plus sqlite_sequence.
//
// sqlite_sequence is not cosmetic. It holds the highest id ever handed out per
// AUTOINCREMENT table, and SQLite lets you write it. Leaving the live value in
// place while replacing the rows underneath it would let a restore from a larger
// database hand out ids that already exist; copying it keeps the counter
// consistent with the rows it counts.
//
// A table missing from the snapshot is skipped rather than emptied. The snapshot
// has already been migrated to the current schema, so this only fires on
// something genuinely unexpected — and skipping loses less than blanking.
//
// schema_migrations is excluded, and that exclusion is load-bearing. It records
// which migration files this database has applied, which is a property of the
// binary that owns the file, not of the data being restored. Copying it lets a
// snapshot taken on a newer build tell an older one that a migration it never
// ran is already applied — and db.Open skips anything already recorded, so that
// migration would then be skipped *forever*, leaving queries referencing a
// column that does not exist. The live table is already correct: the snapshot
// was opened through db.Open and migrated up to this binary's schema before we
// copied a single row.
func restorableTables(ctx context.Context, conn *sql.Conn) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT name FROM main.sqlite_master
		WHERE type = 'table'
		  AND (name NOT LIKE 'sqlite_%' OR name = 'sqlite_sequence')
		  AND name <> 'schema_migrations'
		  AND name IN (SELECT name FROM restore_src.sqlite_master WHERE type = 'table')
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan table name: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("%w: it shares no tables with this database", ErrNotSQLite)
	}
	return tables, nil
}

// quoteIdent quotes a schema identifier with brackets rather than double
// quotes, and that choice is a safety property, not a style one.
//
// SQLite's double-quoted-string misfeature means "foo" silently degrades to the
// *string literal* 'foo' when no column of that name exists. In a
// `SELECT "tags" FROM restore_src.files` over a snapshot missing that column,
// that turns a schema mismatch into a successful copy that writes the text
// "tags" into every row. Brackets have no such fallback: they are always an
// identifier, so the same query fails with "no such column".
func quoteIdent(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

// copyColumns returns the columns to copy for one table, as a bracket-quoted,
// comma-separated list. Naming them explicitly rather than using SELECT * means
// the copy doesn't depend on the two schemas listing their columns in the same
// *order* — only on the same set.
//
// It compares the two schemas up front so a mismatch is reported as itself
// rather than as a bare "no such column" from deep inside the copy. Migrating
// the snapshot should have made this impossible; it is checked because the
// failure it guards against writes wrong data rather than erroring.
func copyColumns(ctx context.Context, tx *sql.Tx, table string) (string, error) {
	live, err := tableColumns(ctx, tx, table, "main")
	if err != nil {
		return "", err
	}
	snapshot, err := tableColumns(ctx, tx, table, "restore_src")
	if err != nil {
		return "", err
	}
	if len(live) == 0 {
		return "", fmt.Errorf("table %s has no columns", table)
	}

	inSnapshot := make(map[string]bool, len(snapshot))
	for _, c := range snapshot {
		inSnapshot[c] = true
	}
	quoted := make([]string, 0, len(live))
	for _, c := range live {
		if !inSnapshot[c] {
			return "", fmt.Errorf("%w: %s.%s is missing", ErrSchemaMismatch, table, c)
		}
		quoted = append(quoted, quoteIdent(c))
	}
	return strings.Join(quoted, ", "), nil
}

func tableColumns(ctx context.Context, tx *sql.Tx, table, schema string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?, ?)`, table, schema)
	if err != nil {
		return nil, fmt.Errorf("columns of %s.%s: %w", schema, table, err)
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan column of %s.%s: %w", schema, table, err)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("columns of %s.%s: %w", schema, table, err)
	}
	return cols, nil
}
