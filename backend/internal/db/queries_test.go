package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shawn-bluce/renderbin/backend/internal/db/sqlcgen"
)

func newQueries(t *testing.T) *sqlcgen.Queries {
	t.Helper()
	conn, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return sqlcgen.New(conn)
}

func createFile(t *testing.T, q *sqlcgen.Queries, slug string) sqlcgen.File {
	t.Helper()
	f, err := q.CreateFile(context.Background(), sqlcgen.CreateFileParams{
		Slug:        slug,
		Name:        "name-" + slug,
		HtmlContent: "<p>" + slug + "</p>",
		Kind:        "html",
		IsPublic:    false,
		AccessCode:  "code-" + slug,
		UserID:      1,
	})
	if err != nil {
		t.Fatalf("CreateFile(%q): %v", slug, err)
	}
	return f
}

func TestGetFileBySlugAnyOwnerFiltersDeleted(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	createFile(t, q, "a")

	got, err := q.GetFileBySlugAnyOwner(ctx, "a")
	if err != nil {
		t.Fatalf("GetFileBySlugAnyOwner: %v", err)
	}
	if got.Slug != "a" {
		t.Errorf("slug = %q, want %q", got.Slug, "a")
	}

	if _, err := q.GetFileBySlugAnyOwner(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetFileBySlugAnyOwner(missing) err = %v, want ErrNoRows", err)
	}

	if _, err := q.SoftDeleteFile(ctx, sqlcgen.SoftDeleteFileParams{
		Slug: "a", UserID: 1,
	}); err != nil {
		t.Fatalf("SoftDeleteFile: %v", err)
	}
	if _, err := q.GetFileBySlugAnyOwner(ctx, "a"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetFileBySlugAnyOwner after delete err = %v, want ErrNoRows", err)
	}
}

func TestListFilesVsDeleted(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	createFile(t, q, "keep")
	createFile(t, q, "trash")
	if _, err := q.SoftDeleteFile(ctx, sqlcgen.SoftDeleteFileParams{
		Slug: "trash", UserID: 1,
	}); err != nil {
		t.Fatalf("SoftDeleteFile: %v", err)
	}

	active, err := q.ListUserFiles(ctx, 1)
	if err != nil {
		t.Fatalf("ListUserFiles: %v", err)
	}
	if len(active) != 1 || active[0].Slug != "keep" {
		t.Errorf("ListUserFiles = %v, want only [keep]", slugsOf(active))
	}

	deleted, err := q.ListUserDeletedFiles(ctx, 1)
	if err != nil {
		t.Fatalf("ListUserDeletedFiles: %v", err)
	}
	if len(deleted) != 1 || deleted[0].Slug != "trash" {
		t.Errorf("ListUserDeletedFiles = %v, want only [trash]", slugsOf(deleted))
	}
}

func TestRestoreFile(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	createFile(t, q, "a")
	if _, err := q.SoftDeleteFile(ctx, sqlcgen.SoftDeleteFileParams{
		Slug: "a", UserID: 1,
	}); err != nil {
		t.Fatalf("SoftDeleteFile: %v", err)
	}

	restored, err := q.RestoreFile(ctx, sqlcgen.RestoreFileParams{Slug: "a", UserID: 1})
	if err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	if restored.DeletedAt.Valid {
		t.Error("restored file should have a null deleted_at")
	}
	if _, err := q.GetFileBySlugAnyOwner(ctx, "a"); err != nil {
		t.Errorf("restored file not visible: %v", err)
	}

	// Restoring a file that isn't deleted matches no row.
	if _, err := q.RestoreFile(ctx, sqlcgen.RestoreFileParams{Slug: "a", UserID: 1}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("RestoreFile on active file err = %v, want ErrNoRows", err)
	}
}

func TestUpdateFileChangesSlugNameContent(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	createFile(t, q, "old")

	updated, err := q.UpdateFile(ctx, sqlcgen.UpdateFileParams{
		Name:        "new name",
		NewSlug:     "new-slug",
		HtmlContent: "<h1>v2</h1>",
		AccessCode:  "code-new",
		Slug:        "old", // WHERE (old) slug
		UserID:      1,
	})
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if updated.Slug != "new-slug" || updated.Name != "new name" || updated.HtmlContent != "<h1>v2</h1>" {
		t.Errorf("UpdateFile result = %+v", updated)
	}
	if _, err := q.GetFileBySlugAnyOwner(ctx, "old"); !errors.Is(err, sql.ErrNoRows) {
		t.Error("old slug should no longer resolve")
	}
	if _, err := q.GetFileBySlugAnyOwner(ctx, "new-slug"); err != nil {
		t.Errorf("new slug should resolve: %v", err)
	}
}

func TestUpdateFileDuplicateSlugFails(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	createFile(t, q, "a")
	createFile(t, q, "b")

	_, err := q.UpdateFile(ctx, sqlcgen.UpdateFileParams{
		Name:        "x",
		NewSlug:     "a", // collide with existing slug "a"
		HtmlContent: "<p>x</p>",
		AccessCode:  "code-x",
		Slug:        "b",
		UserID:      1,
	})
	if err == nil {
		t.Fatal("expected a uniqueness error, got nil")
	}
	if !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("error = %v, want it to mention UNIQUE", err)
	}
}

func TestSetAndClearExpiry(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	createFile(t, q, "a") // starts private

	withViews, err := q.SetFileExpiry(ctx, sqlcgen.SetFileExpiryParams{
		MaxViews: sql.NullInt64{Int64: 5, Valid: true},
		Slug:     "a",
		UserID:   1,
	})
	if err != nil {
		t.Fatalf("SetFileExpiry: %v", err)
	}
	if !withViews.IsPublic {
		t.Error("setting an expiry limit must force the file public")
	}
	if !withViews.MaxViews.Valid || withViews.MaxViews.Int64 != 5 {
		t.Errorf("MaxViews = %+v, want {5 true}", withViews.MaxViews)
	}
	if withViews.ViewCount != 0 {
		t.Errorf("ViewCount = %d, want 0 after setting a limit", withViews.ViewCount)
	}

	if err := q.IncrementFileViewCount(ctx, "a"); err != nil {
		t.Fatalf("IncrementFileViewCount: %v", err)
	}

	cleared, err := q.ClearFileExpiry(ctx, sqlcgen.ClearFileExpiryParams{Slug: "a", UserID: 1})
	if err != nil {
		t.Fatalf("ClearFileExpiry: %v", err)
	}
	if cleared.MaxViews.Valid || cleared.ExpiresAt.Valid {
		t.Error("ClearFileExpiry should null out both limit columns")
	}
	if cleared.ViewCount != 0 {
		t.Errorf("ViewCount = %d, want 0 after clear", cleared.ViewCount)
	}
	if !cleared.IsPublic {
		t.Error("ClearFileExpiry must not change visibility (was public)")
	}
}

func TestExpireFileGoesPrivateAndClearsLimits(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	createFile(t, q, "a")
	if _, err := q.SetFileExpiry(ctx, sqlcgen.SetFileExpiryParams{
		ExpiresAt: sql.NullTime{Time: time.Now().Add(time.Hour), Valid: true},
		Slug:      "a",
		UserID:    1,
	}); err != nil {
		t.Fatalf("SetFileExpiry: %v", err)
	}

	if err := q.ExpireFile(ctx, "a"); err != nil {
		t.Fatalf("ExpireFile: %v", err)
	}
	got, err := q.GetFileBySlugAnyOwner(ctx, "a")
	if err != nil {
		t.Fatalf("GetFileBySlugAnyOwner: %v", err)
	}
	if got.IsPublic {
		t.Error("expired file should be private")
	}
	if got.ExpiresAt.Valid || got.MaxViews.Valid {
		t.Error("expired file should have its limit columns cleared")
	}
}

func TestIncrementCounts(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	createFile(t, q, "a")

	if err := q.IncrementFileSuccessCount(ctx, "a"); err != nil {
		t.Fatalf("IncrementFileSuccessCount: %v", err)
	}
	if err := q.IncrementFileFailureCount(ctx, "a"); err != nil {
		t.Fatalf("IncrementFileFailureCount: %v", err)
	}
	if err := q.IncrementFileFailureCount(ctx, "a"); err != nil {
		t.Fatalf("IncrementFileFailureCount: %v", err)
	}
	got, err := q.GetFileBySlugAnyOwner(ctx, "a")
	if err != nil {
		t.Fatalf("GetFileBySlugAnyOwner: %v", err)
	}
	if got.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", got.SuccessCount)
	}
	if got.FailureCount != 2 {
		t.Errorf("FailureCount = %d, want 2", got.FailureCount)
	}
}

func TestSessionLifecycle(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()

	// Use a margin larger than any timezone offset: the driver stores the
	// wall-clock time and GetValidSession compares it against UTC
	// CURRENT_TIMESTAMP, so ±1h would be ambiguous under a skewed TZ.
	if err := q.CreateSession(ctx, sqlcgen.CreateSessionParams{
		Token:     "valid",
		UserID:    1,
		ExpiresAt: time.Now().Add(48 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession(valid): %v", err)
	}
	if err := q.CreateSession(ctx, sqlcgen.CreateSessionParams{
		Token:     "expired",
		UserID:    1,
		ExpiresAt: time.Now().Add(-48 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession(expired): %v", err)
	}

	if _, err := q.GetValidSession(ctx, "valid"); err != nil {
		t.Errorf("GetValidSession(valid) err = %v, want nil", err)
	}
	if _, err := q.GetValidSession(ctx, "expired"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetValidSession(expired) err = %v, want ErrNoRows", err)
	}

	// DeleteExpiredSessions removes only the expired one.
	if err := q.DeleteExpiredSessions(ctx); err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if _, err := q.GetValidSession(ctx, "valid"); err != nil {
		t.Errorf("valid session removed by expired-sweep: %v", err)
	}

	// DeleteSession removes a specific token.
	if err := q.DeleteSession(ctx, "valid"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := q.GetValidSession(ctx, "valid"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetValidSession after delete err = %v, want ErrNoRows", err)
	}
}

func TestUserLifecycle(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()

	if n, err := q.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("CountUsers = %d, %v; want 0, nil", n, err)
	}

	first, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     "admin",
		Nickname:     "Boss",
		PasswordHash: "hash",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if first.ID != 1 {
		t.Errorf("first user id = %d, want 1 (super admin)", first.ID)
	}

	// Duplicate usernames are rejected.
	if _, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: "admin", Nickname: "x", PasswordHash: "h",
	}); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("duplicate username err = %v, want UNIQUE violation", err)
	}

	byName, err := q.GetUserByUsername(ctx, "admin")
	if err != nil || byName.ID != first.ID {
		t.Errorf("GetUserByUsername = %+v, %v", byName, err)
	}

	if _, err := q.UpdateUserNickname(ctx, sqlcgen.UpdateUserNicknameParams{
		Nickname: "Renamed", ID: first.ID,
	}); err != nil {
		t.Fatalf("UpdateUserNickname: %v", err)
	}
	if err := q.UpdateUserPassword(ctx, sqlcgen.UpdateUserPasswordParams{
		PasswordHash: "hash2", ID: first.ID,
	}); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}
	if err := q.SetUserAPIKey(ctx, sqlcgen.SetUserAPIKeyParams{
		ApiKey: sql.NullString{String: "rb_abc", Valid: true}, ID: first.ID,
	}); err != nil {
		t.Fatalf("SetUserAPIKey: %v", err)
	}

	got, err := q.GetUserByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Nickname != "Renamed" || got.PasswordHash != "hash2" || got.ApiKey.String != "rb_abc" {
		t.Errorf("user after updates = %+v", got)
	}
}

func TestConfigSetOverwrites(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()

	if _, err := q.GetConfig(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetConfig(missing) err = %v, want ErrNoRows", err)
	}

	if err := q.SetConfig(ctx, sqlcgen.SetConfigParams{Key: "k", Value: "true"}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if err := q.SetConfig(ctx, sqlcgen.SetConfigParams{Key: "k", Value: "false"}); err != nil {
		t.Fatalf("SetConfig (overwrite): %v", err)
	}
	if v, err := q.GetConfig(ctx, "k"); err != nil || v != "false" {
		t.Errorf("GetConfig = %q, %v; want false, nil", v, err)
	}
}

func slugsOf(files []sqlcgen.File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Slug
	}
	return out
}

// TestFileQueriesRejectForeignOwner pins owner isolation at the layer that
// actually enforces it. Handlers turn "no row" into 404; this checks that the
// SQL produces no row in the first place, for every scoped query.
func TestFileQueriesRejectForeignOwner(t *testing.T) {
	const owner, stranger = 1, 2
	ctx := context.Background()

	// Each case runs against a fresh DB holding one file owned by user 1,
	// so a leak in one query can't be masked by an earlier one.
	cases := []struct {
		name string
		// run performs the query as the stranger and reports whether it
		// matched a row.
		run func(t *testing.T, q *sqlcgen.Queries) bool
	}{
		{"GetUserFileBySlug", func(t *testing.T, q *sqlcgen.Queries) bool {
			_, err := q.GetUserFileBySlug(ctx, sqlcgen.GetUserFileBySlugParams{Slug: "a", UserID: stranger})
			return !errors.Is(err, sql.ErrNoRows)
		}},
		{"RenameFile", func(t *testing.T, q *sqlcgen.Queries) bool {
			_, err := q.RenameFile(ctx, sqlcgen.RenameFileParams{Name: "hacked", Slug: "a", UserID: stranger})
			return !errors.Is(err, sql.ErrNoRows)
		}},
		{"SetFileVisibility", func(t *testing.T, q *sqlcgen.Queries) bool {
			_, err := q.SetFileVisibility(ctx, sqlcgen.SetFileVisibilityParams{IsPublic: true, Slug: "a", UserID: stranger})
			return !errors.Is(err, sql.ErrNoRows)
		}},
		{"SetFileTags", func(t *testing.T, q *sqlcgen.Queries) bool {
			_, err := q.SetFileTags(ctx, sqlcgen.SetFileTagsParams{Tags: "hacked", Slug: "a", UserID: stranger})
			return !errors.Is(err, sql.ErrNoRows)
		}},
		{"RefreshFileAccessCode", func(t *testing.T, q *sqlcgen.Queries) bool {
			_, err := q.RefreshFileAccessCode(ctx, sqlcgen.RefreshFileAccessCodeParams{AccessCode: "hacked", Slug: "a", UserID: stranger})
			return !errors.Is(err, sql.ErrNoRows)
		}},
		{"UpdateFile", func(t *testing.T, q *sqlcgen.Queries) bool {
			_, err := q.UpdateFile(ctx, sqlcgen.UpdateFileParams{
				Name: "hacked", NewSlug: "hacked", HtmlContent: "<p>hacked</p>",
				AccessCode: "hacked", Slug: "a", UserID: stranger,
			})
			return !errors.Is(err, sql.ErrNoRows)
		}},
		{"SetFileExpiry", func(t *testing.T, q *sqlcgen.Queries) bool {
			_, err := q.SetFileExpiry(ctx, sqlcgen.SetFileExpiryParams{
				MaxViews: sql.NullInt64{Int64: 1, Valid: true}, Slug: "a", UserID: stranger,
			})
			return !errors.Is(err, sql.ErrNoRows)
		}},
		{"ClearFileExpiry", func(t *testing.T, q *sqlcgen.Queries) bool {
			_, err := q.ClearFileExpiry(ctx, sqlcgen.ClearFileExpiryParams{Slug: "a", UserID: stranger})
			return !errors.Is(err, sql.ErrNoRows)
		}},
		{"SoftDeleteFile", func(t *testing.T, q *sqlcgen.Queries) bool {
			rows, err := q.SoftDeleteFile(ctx, sqlcgen.SoftDeleteFileParams{Slug: "a", UserID: stranger})
			if err != nil {
				t.Fatalf("SoftDeleteFile: %v", err)
			}
			return rows != 0
		}},
		{"ListUserFiles", func(t *testing.T, q *sqlcgen.Queries) bool {
			rows, err := q.ListUserFiles(ctx, stranger)
			if err != nil {
				t.Fatalf("ListUserFiles: %v", err)
			}
			return len(rows) != 0
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := newQueries(t)
			createFile(t, q, "a") // owned by user 1
			if c.run(t, q) {
				t.Errorf("%s matched user %d's file as user %d", c.name, owner, stranger)
			}
			// The row must be exactly as created.
			got, err := q.GetFileBySlugAnyOwner(ctx, "a")
			if err != nil {
				t.Fatalf("file should still exist: %v", err)
			}
			if got.Name != "name-a" || got.HtmlContent != "<p>a</p>" || got.AccessCode != "code-a" ||
				got.IsPublic || got.Tags != "" || got.DeletedAt.Valid {
				t.Errorf("file was mutated by a foreign-owner query: %+v", got)
			}
		})
	}

	// Trash-only queries need a soft-deleted file to be meaningful.
	for _, c := range []struct {
		name string
		run  func(t *testing.T, q *sqlcgen.Queries) bool
	}{
		{"RestoreFile", func(t *testing.T, q *sqlcgen.Queries) bool {
			_, err := q.RestoreFile(ctx, sqlcgen.RestoreFileParams{Slug: "a", UserID: stranger})
			return !errors.Is(err, sql.ErrNoRows)
		}},
		{"HardDeleteFile", func(t *testing.T, q *sqlcgen.Queries) bool {
			rows, err := q.HardDeleteFile(ctx, sqlcgen.HardDeleteFileParams{Slug: "a", UserID: stranger})
			if err != nil {
				t.Fatalf("HardDeleteFile: %v", err)
			}
			return rows != 0
		}},
		{"ListUserDeletedFiles", func(t *testing.T, q *sqlcgen.Queries) bool {
			rows, err := q.ListUserDeletedFiles(ctx, stranger)
			if err != nil {
				t.Fatalf("ListUserDeletedFiles: %v", err)
			}
			return len(rows) != 0
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			q := newQueries(t)
			createFile(t, q, "a")
			if _, err := q.SoftDeleteFile(ctx, sqlcgen.SoftDeleteFileParams{Slug: "a", UserID: owner}); err != nil {
				t.Fatalf("SoftDeleteFile: %v", err)
			}
			if c.run(t, q) {
				t.Errorf("%s matched user %d's trashed file as user %d", c.name, owner, stranger)
			}
			// Still in the owner's trash: neither restored nor purged.
			rows, err := q.ListUserDeletedFiles(ctx, owner)
			if err != nil {
				t.Fatalf("ListUserDeletedFiles: %v", err)
			}
			if len(rows) != 1 || rows[0].Slug != "a" {
				t.Errorf("owner's trash = %v, want [a]", slugsOf(rows))
			}
		})
	}
}
