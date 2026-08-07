// Package backup produces consistent, restorable snapshots of the live
// SQLite database. It uses SQLite's VACUUM INTO, which writes a fresh
// standalone database file (no WAL sidecar, fully checkpointed) that can be
// restored by simply replacing app.db — safe to run against the live
// connection without stopping the server.
package backup

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Snapshot writes a consistent copy of db to destPath using VACUUM INTO.
// destPath must not already exist (SQLite refuses to overwrite it).
func Snapshot(ctx context.Context, db *sql.DB, destPath string) error {
	// VACUUM INTO takes a string literal, not a bound parameter, so the path
	// is interpolated. destPath is server-controlled (a temp path), never user
	// input; single quotes are still escaped defensively.
	escaped := strings.ReplaceAll(destPath, "'", "''")
	if _, err := db.ExecContext(ctx, "VACUUM INTO '"+escaped+"'"); err != nil {
		return fmt.Errorf("vacuum into %s: %w", destPath, err)
	}
	return nil
}
