-- Storage accounting (a per-file byte size and a per-user quota) plus the
-- indexes the owner-scoped listings always needed.
--
-- Keep this file pure ASCII -- sqlc reads this directory for its schema, and
-- its SQLite rewriter works on byte offsets while the parser reports character
-- positions, so one multi-byte character silently corrupts codegen.

-- files.content_size is html_content's length in bytes, maintained by the
-- INSERT and UPDATE statements themselves (see queries/files.sql) rather than
-- by Go, so no call site can set the content without setting the size.
--
-- It exists because the dashboard listing used to be SELECT *: it pulled every
-- document's full source out of SQLite only for the handler to blank the field
-- before encoding. An account holding 160MB of files drove the server to 350MB
-- RSS to answer a 13KB list request. Reading a size column instead keeps that
-- path proportional to the number of files rather than to their contents, and
-- it is what makes the quota sum below cheap enough to run per upload.
ALTER TABLE files ADD COLUMN content_size INTEGER NOT NULL DEFAULT 0;

-- Backfill. length(CAST(x AS BLOB)) is bytes; bare length() on TEXT would
-- count characters and undercount every non-ASCII document.
UPDATE files SET content_size = length(CAST(html_content AS BLOB));

-- The two triggers below make content_size self-correcting, so the column is
-- right no matter which binary wrote the row.
--
-- queries/files.sql already derives it in the INSERT and UPDATE themselves, so
-- for this version's own writers the WHEN clause is false and these never fire.
-- They exist for a writer that does not know about the column at all: run an
-- older image against an upgraded database -- which works, because sqlc expands
-- SELECT * into explicit column names at generation time, so an older binary
-- simply ignores columns added later -- and its INSERT takes the DEFAULT 0.
-- Rolling forward again would not repair those rows (0003 is already recorded,
-- so the backfill above never runs a second time), leaving files that report
-- 0 bytes and count nothing against their owner's quota.
--
-- AFTER UPDATE **OF html_content** matters: a plain AFTER UPDATE would also fire
-- on the view-counter bumps that /res/{slug} issues on every single request.
CREATE TRIGGER files_content_size_after_insert
AFTER INSERT ON files
WHEN NEW.content_size <> length(CAST(NEW.html_content AS BLOB))
BEGIN
    UPDATE files SET content_size = length(CAST(NEW.html_content AS BLOB))
    WHERE id = NEW.id;
END;

CREATE TRIGGER files_content_size_after_update
AFTER UPDATE OF html_content ON files
WHEN NEW.content_size <> length(CAST(NEW.html_content AS BLOB))
BEGIN
    UPDATE files SET content_size = length(CAST(NEW.html_content AS BLOB))
    WHERE id = NEW.id;
END;

-- Per-account storage limit, default 100MB. Enforced on upload and update
-- against the sum of the account's content_size (trash included -- a trashed
-- file still occupies the database, and "empty the trash" is a user action).
-- The super admin can change any account's limit; 0 is not special-cased, so
-- setting it blocks further uploads without touching what is already stored.
ALTER TABLE users ADD COLUMN quota_bytes INTEGER NOT NULL DEFAULT 104857600;

-- Every file query reachable from a request filters on user_id, but no index
-- carried it: idx_files_list was (created_at) WHERE deleted_at IS NULL, which
-- could order the scan but never narrow it to one owner. This one does both,
-- for the active listing, the trash listing and the quota sum alike.
CREATE INDEX idx_files_owner_list ON files (user_id, deleted_at, created_at);

-- Superseded by the index above for every query that used it; dropping it
-- saves the write amplification of maintaining two indexes on the same table.
DROP INDEX IF EXISTS idx_files_list;

-- DeleteUserSessions (suspension, admin password reset, account deletion) had
-- to scan the whole table to find one account's rows.
CREATE INDEX idx_sessions_user_id ON sessions (user_id);
