-- name: CreateUser :one
INSERT INTO users (username, nickname, password_hash)
VALUES (?, ?, ?)
RETURNING *;

-- The first-run account, and the reason it is a separate statement: the
-- welcome flow used to read CountUsers, decide the table was empty, spend
-- ~80ms hashing a password, and only then INSERT. Six concurrent POSTs to
-- /api/setup all passed the check and created six accounts, of which only id=1
-- got the (non-transferable) super-admin role -- so a double-clicked form left
-- a stray account and an exposed instance could be raced for ownership.
-- WHERE NOT EXISTS moves the decision into the write itself: the loser of the
-- race matches no row and the handler turns that into the same 409 a late
-- sequential request already got.
-- name: CreateFirstUser :one
INSERT INTO users (username, nickname, password_hash)
SELECT sqlc.arg(username), sqlc.arg(nickname), sqlc.arg(password_hash)
WHERE NOT EXISTS (SELECT 1 FROM users)
RETURNING *;

-- name: GetUserByUsername :one
SELECT * FROM users
WHERE username = ?;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = ?;

-- name: GetUserByAPIKey :one
SELECT * FROM users
WHERE api_key = ?;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- The account-management list. File counts come from correlated subqueries
-- rather than a GROUP BY join so a user with no files still appears (with 0)
-- and the active/trashed split stays one row per user. used_bytes sums the
-- stored content_size column rather than measuring html_content, so listing
-- every account does not read every account's documents.
-- name: ListUsersWithFileCounts :many
SELECT users.*,
    (SELECT COUNT(*) FROM files
     WHERE files.user_id = users.id AND files.deleted_at IS NULL) AS file_count,
    (SELECT COUNT(*) FROM files
     WHERE files.user_id = users.id AND files.deleted_at IS NOT NULL) AS trashed_count,
    CAST((SELECT COALESCE(SUM(content_size), 0) FROM files
     WHERE files.user_id = users.id) AS INTEGER) AS used_bytes
FROM users
ORDER BY users.id;

-- name: UpdateUserQuota :one
UPDATE users
SET quota_bytes = sqlc.arg(quota_bytes), updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id)
RETURNING *;

-- Account deletion. Callers must delete the account's files and sessions in
-- the same transaction (there are no foreign keys to cascade for us), and the
-- handler must refuse id=1: the super admin is the only account that can
-- manage accounts, so removing it would be a one-way door out of the app.
-- name: DeleteUser :execrows
DELETE FROM users
WHERE id = sqlc.arg(id);

-- name: UpdateUserNickname :one
UPDATE users
SET nickname = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- :execrows so the admin reset path can tell "changed it" from "no such user";
-- the self-service path in settings.go already knows the user exists.
-- name: UpdateUserPassword :execrows
UPDATE users
SET password_hash = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- COALESCE keeps the original suspension time, so re-suspending an already
-- suspended account is a no-op rather than a way to lose when it happened.
-- name: DisableUser :one
UPDATE users
SET disabled_at = COALESCE(disabled_at, CURRENT_TIMESTAMP), updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: EnableUser :one
UPDATE users
SET disabled_at = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: SetUserAPIKey :exec
UPDATE users
SET api_key = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;
