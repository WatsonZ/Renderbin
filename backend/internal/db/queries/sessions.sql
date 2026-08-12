-- name: CreateSession :exec
INSERT INTO sessions (token, user_id, expires_at)
VALUES (?, ?, ?);

-- Expiry is compared through unixepoch() rather than as text: this column holds
-- an ISO-8601 UTC string written by the driver, CURRENT_TIMESTAMP produces a
-- bare 'YYYY-MM-DD HH:MM:SS', and a plain '>' between the two would be a
-- lexicographic comparison of two different shapes. unixepoch() reduces both to
-- an instant. A value it cannot parse yields NULL, so such a row is never valid
-- -- the fail-closed direction.
-- name: GetValidSession :one
SELECT * FROM sessions
WHERE token = ? AND unixepoch(expires_at) > unixepoch('now');

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE token = ?;

-- Every session of one user, for the privileged actions that must end a
-- suspended or password-reset account's access right now rather than whenever
-- its cookies happen to expire.
-- name: DeleteUserSessions :exec
DELETE FROM sessions
WHERE user_id = ?;

-- The IS NULL arm sweeps rows whose expires_at unixepoch() cannot parse:
-- GetValidSession already treats them as expired, and without this they would
-- never be collected.
-- name: DeleteExpiredSessions :exec
DELETE FROM sessions
WHERE unixepoch(expires_at) IS NULL OR unixepoch(expires_at) <= unixepoch('now');
