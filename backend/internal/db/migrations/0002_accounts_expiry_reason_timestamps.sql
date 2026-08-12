-- Account suspension, the "why did this link go offline" marker, and a one-off
-- rewrite of the timestamps an earlier build stored in Go's own format.
--
-- Forward-only: never edit this file once it has shipped. Keep it pure ASCII --
-- sqlc reads this directory as its schema, and its SQLite rewriter substitutes
-- parameters by byte offset while the parser reports character positions, so a
-- single multi-byte character silently corrupts codegen.

-- Suspended accounts; NULL means active. A suspended user cannot log in, their
-- existing sessions stop resolving (CurrentUser rejects them), their API key
-- stops working, and their files stop being served at /res/{slug} --
-- GetFileBySlugAnyOwner joins this column so a suspended owner's link is
-- indistinguishable from a slug that never existed. The super admin (id=1) can
-- never be suspended; handlers enforce that, since the alternative is locking
-- everyone out of the global settings with no way back.
ALTER TABLE users ADD COLUMN disabled_at TIMESTAMP;

-- Why a public file was last taken offline by the lazy expiry check in
-- FilesHandler.Render: 'ttl' (the time window passed) or 'views' (the view
-- quota ran out). Only the latest event is kept -- each expiry overwrites the
-- previous one -- and configuring or clearing an expiry wipes it, so the
-- marker never contradicts a live limit sitting next to it.
ALTER TABLE files ADD COLUMN expired_at TIMESTAMP;
ALTER TABLE files ADD COLUMN expired_reason TEXT NOT NULL DEFAULT '';

-- Timestamp normalisation.
--
-- Until now the SQLite DSN left the driver's write format at its default,
-- which is Go's time.Time.String():
--
--   2026-09-09 09:40:21.365288 +0800 CST m=+2592020.551943710
--
-- ...local wall clock, with the monotonic clock reading appended. Nothing else
-- in the database speaks that: CURRENT_TIMESTAMP writes 'YYYY-MM-DD HH:MM:SS'
-- in UTC, so any SQL comparison between the two mixed formats and time zones.
-- The DSN now pins _time_format=sqlite&_timezone=UTC, and the two Go-written
-- columns are rewritten here to match.
--
-- The rewrite preserves the instant and drops sub-second precision (irrelevant
-- for an expiry): take the leading 'YYYY-MM-DD HH:MM:SS', re-attach the numeric
-- offset as '+HH:MM' so SQLite's date functions can read it, then let strftime
-- convert to UTC. ' +' / ' -' (with the leading space) identifies the offset
-- token: a date's own hyphens are never preceded by a space, and the new format
-- has no space at all. COALESCE keeps the original value if strftime returns
-- NULL on something unexpected, so a surprise can never null out a NOT NULL
-- column and fail the migration -- a session row that survives unparsed simply
-- reads as expired and its owner logs in again.

UPDATE files
SET expires_at = COALESCE(
        strftime('%Y-%m-%d %H:%M:%S',
            substr(expires_at, 1, 19) ||
            CASE WHEN instr(expires_at, ' +') > 0
                 THEN substr(expires_at, instr(expires_at, ' +') + 1, 3) || ':' ||
                      substr(expires_at, instr(expires_at, ' +') + 4, 2)
                 ELSE substr(expires_at, instr(expires_at, ' -') + 1, 3) || ':' ||
                      substr(expires_at, instr(expires_at, ' -') + 4, 2)
            END) || '+00:00',
        expires_at)
WHERE expires_at IS NOT NULL
  AND (instr(expires_at, ' +') > 0 OR instr(expires_at, ' -') > 0);

UPDATE sessions
SET expires_at = COALESCE(
        strftime('%Y-%m-%d %H:%M:%S',
            substr(expires_at, 1, 19) ||
            CASE WHEN instr(expires_at, ' +') > 0
                 THEN substr(expires_at, instr(expires_at, ' +') + 1, 3) || ':' ||
                      substr(expires_at, instr(expires_at, ' +') + 4, 2)
                 ELSE substr(expires_at, instr(expires_at, ' -') + 1, 3) || ':' ||
                      substr(expires_at, instr(expires_at, ' -') + 4, 2)
            END) || '+00:00',
        expires_at)
WHERE instr(expires_at, ' +') > 0 OR instr(expires_at, ' -') > 0;
