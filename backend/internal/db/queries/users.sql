-- name: CreateUser :one
INSERT INTO users (username, nickname, password_hash)
VALUES (?, ?, ?)
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

-- name: UpdateUserNickname :one
UPDATE users
SET nickname = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: SetUserAPIKey :exec
UPDATE users
SET api_key = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;
