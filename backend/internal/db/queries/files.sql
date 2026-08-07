-- ===========================================================================
-- OWNER-SCOPED -- everything reachable from a request behind requireAuth.
--
-- Every query below carries `user_id = sqlc.arg(user_id)`, so a slug belonging
-- to another user simply doesn't match: reads return sql.ErrNoRows and writes
-- affect 0 rows, which handlers turn into the same 404 a nonexistent slug
-- gets. Ownership therefore lives in the schema, not in a Go check a caller
-- can forget -- and because sqlc runs with emit_interface: false, dropping the
-- predicate from a query breaks the build at every call site.
--
-- New file queries belong in this section and MUST carry the predicate.
-- ===========================================================================

-- name: CreateFile :one
INSERT INTO files (slug, name, html_content, kind, is_public, access_code, user_id)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListUserFiles :many
SELECT * FROM files
WHERE deleted_at IS NULL AND user_id = sqlc.arg(user_id)
ORDER BY created_at DESC;

-- name: ListUserDeletedFiles :many
SELECT * FROM files
WHERE deleted_at IS NOT NULL AND user_id = sqlc.arg(user_id)
ORDER BY deleted_at DESC;

-- name: GetUserFileBySlug :one
SELECT * FROM files
WHERE slug = sqlc.arg(slug) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL;

-- Plain substring search via instr() rather than LIKE: no wildcard/escape
-- handling needed, and lower() gives the same ASCII-only case-insensitivity
-- LIKE would.

-- name: SearchUserFilesByName :many
SELECT * FROM files
WHERE deleted_at IS NULL AND user_id = sqlc.arg(user_id)
  AND instr(lower(name), lower(sqlc.arg(name_query))) > 0
ORDER BY created_at DESC;

-- name: SearchUserFilesWithContent :many
SELECT * FROM files
WHERE deleted_at IS NULL AND user_id = sqlc.arg(user_id)
  AND (instr(lower(name), lower(sqlc.arg(name_query))) > 0
    OR instr(lower(html_content), lower(sqlc.arg(content_query))) > 0)
ORDER BY created_at DESC;

-- name: RenameFile :one
UPDATE files
SET name = sqlc.arg(name), updated_at = CURRENT_TIMESTAMP
WHERE slug = sqlc.arg(slug) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL
RETURNING *;

-- name: SetFileVisibility :one
UPDATE files
SET is_public = sqlc.arg(is_public), updated_at = CURRENT_TIMESTAMP
WHERE slug = sqlc.arg(slug) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL
RETURNING *;

-- name: SetFileTags :one
UPDATE files
SET tags = sqlc.arg(tags), updated_at = CURRENT_TIMESTAMP
WHERE slug = sqlc.arg(slug) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL
RETURNING *;

-- name: RefreshFileAccessCode :one
UPDATE files
SET access_code = sqlc.arg(access_code), updated_at = CURRENT_TIMESTAMP
WHERE slug = sqlc.arg(slug) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL
RETURNING *;

-- The SET slug and the WHERE slug are different values; naming both explicitly
-- keeps them from being mixed up (and avoids sqlc's generated `Slug_2` field).
-- name: UpdateFile :one
UPDATE files
SET name = sqlc.arg(name),
    slug = sqlc.arg(new_slug),
    html_content = sqlc.arg(html_content),
    access_code = sqlc.arg(access_code),
    updated_at = CURRENT_TIMESTAMP
WHERE slug = sqlc.arg(slug) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL
RETURNING *;

-- name: SetFileExpiry :one
UPDATE files
SET is_public = 1,
    expires_at = sqlc.arg(expires_at),
    max_views = sqlc.arg(max_views),
    view_count = 0,
    updated_at = CURRENT_TIMESTAMP
WHERE slug = sqlc.arg(slug) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL
RETURNING *;

-- name: ClearFileExpiry :one
UPDATE files
SET expires_at = NULL, max_views = NULL, view_count = 0, updated_at = CURRENT_TIMESTAMP
WHERE slug = sqlc.arg(slug) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL
RETURNING *;

-- :execrows rather than :exec so the handler can tell "soft-deleted it" from
-- "matched nothing" and 404 instead of reporting a success-shaped no-op.
-- name: SoftDeleteFile :execrows
UPDATE files
SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE slug = sqlc.arg(slug) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL;

-- name: RestoreFile :one
UPDATE files
SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP
WHERE slug = sqlc.arg(slug) AND user_id = sqlc.arg(user_id) AND deleted_at IS NOT NULL
RETURNING *;

-- name: HardDeleteFile :execrows
DELETE FROM files
WHERE slug = sqlc.arg(slug) AND user_id = sqlc.arg(user_id) AND deleted_at IS NOT NULL;

-- ===========================================================================
-- UNSCOPED -- the public render path (GET /res/{slug}) only.
--
-- These serve anonymous visitors holding a share link, who have no user at
-- all, so they cannot be owner-scoped without breaking every public link.
-- NEVER call anything below this line from a handler behind requireAuth --
-- that is exactly how the IDOR this section exists to prevent was introduced.
-- See FilesHandler.Render.
-- ===========================================================================

-- name: GetFileBySlugAnyOwner :one
SELECT * FROM files
WHERE slug = ? AND deleted_at IS NULL;

-- name: IncrementFileSuccessCount :exec
UPDATE files
SET success_count = success_count + 1
WHERE slug = ? AND deleted_at IS NULL;

-- name: IncrementFileCodeSuccessCount :exec
UPDATE files
SET code_success_count = code_success_count + 1
WHERE slug = ? AND deleted_at IS NULL;

-- name: IncrementFileFailureCount :exec
UPDATE files
SET failure_count = failure_count + 1
WHERE slug = ? AND deleted_at IS NULL;

-- name: IncrementFileViewCount :exec
UPDATE files
SET view_count = view_count + 1
WHERE slug = ? AND deleted_at IS NULL;

-- name: ExpireFile :exec
UPDATE files
SET is_public = 0, expires_at = NULL, max_views = NULL
WHERE slug = ? AND deleted_at IS NULL;
