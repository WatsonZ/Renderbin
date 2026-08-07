package handlers

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/shawn-bluce/renderbin/backend/internal/backup"
)

// BackupHandler serves a downloadable snapshot of the SQLite database.
type BackupHandler struct {
	db     *sql.DB
	logger *slog.Logger
}

func NewBackupHandler(db *sql.DB, logger *slog.Logger) *BackupHandler {
	return &BackupHandler{db: db, logger: logger}
}

// Download streams a consistent SQLite snapshot at GET /api/backup. The
// snapshot is a standalone .db file (produced via VACUUM INTO into a temp
// dir), restorable by replacing app.db.
//
// Super-admin only, not merely signed-in: the snapshot is the whole database,
// so it carries every user's files, password hash and MCP API key. 403 rather
// than 404 because this path is fixed and public (the SPA links to it) — only
// the caller's privilege is in question, not the endpoint's existence.
func (h *BackupHandler) Download(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !IsSuperAdmin(user) {
		http.Error(w, "super admin only", http.StatusForbidden)
		return
	}

	dir, err := os.MkdirTemp("", "renderbin-backup-")
	if err != nil {
		h.logger.Error("backup temp dir", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(dir)

	dest := filepath.Join(dir, "backup.db")
	if err := backup.Snapshot(r.Context(), h.db, dest); err != nil {
		h.logger.Error("backup snapshot", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	f, err := os.Open(dest)
	if err != nil {
		h.logger.Error("open backup snapshot", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	filename := fmt.Sprintf("renderbin-backup-%s.db", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	if stat, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	}

	if _, err := io.Copy(w, f); err != nil {
		// Headers (200) are already sent, so we can't change the status now;
		// just log the truncated transfer.
		h.logger.Warn("stream backup", "error", err)
	}
}
