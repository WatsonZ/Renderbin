-- name: GetConfig :one
SELECT value FROM configs
WHERE key = ?;

-- name: SetConfig :exec
INSERT INTO configs (key, value)
VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value;
