-- Initial schema.
--
-- Squashed from the seven incremental migrations that existed before the first
-- release. Column order is preserved exactly as those ALTER TABLEs left it, so
-- this describes a database identical to one migrated the long way; it just
-- isn't the order you would pick writing it fresh.
--
-- Migrations are forward-only: add a new numbered file (0002_*.sql), never
-- edit this one once it has shipped. Keep every file here pure ASCII -- sqlc
-- reads this directory for its schema, and its rewriter works on byte offsets
-- while the parser reports character positions, so one multi-byte character
-- silently corrupts codegen.

-- Users. The first row created (id=1, via the first-run welcome page) is the
-- super admin by convention -- handlers.SuperAdminID. There are no roles
-- beyond that.
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    nickname TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    -- Per-user MCP API key, stored in plaintext (a deliberate, recorded
    -- deferral). NULL until MCP is enabled and the user first visits
    -- settings; created lazily and reused thereafter.
    api_key TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Partial index: api_key is unique when set, but most rows may have none.
CREATE UNIQUE INDEX idx_users_api_key ON users (api_key) WHERE api_key IS NOT NULL;

-- Generic key/value app configuration. Current keys: allow_registration,
-- mcp_enabled. Values are TEXT; booleans use 'true'/'false' and read as false
-- when the row is missing.
CREATE TABLE configs (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Uploaded documents. html_content holds the raw source of every kind; how it
-- is served at /res/{slug} depends on kind. user_id is the owner, and every
-- query reachable from an authenticated request filters on it.
CREATE TABLE files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    html_content TEXT NOT NULL,
    is_public BOOLEAN NOT NULL DEFAULT 0,
    access_code TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- Soft delete: the trash. A non-NULL value hides the row everywhere,
    -- including from its own owner's /res/{slug} requests.
    deleted_at TIMESTAMP,
    -- Access analytics, split by how access was granted. Bumped on every
    -- request to an existing file; deliberately does not touch updated_at,
    -- since counting a view is not editing the document.
    success_count INTEGER NOT NULL DEFAULT 0,      -- viewed by its owner
    failure_count INTEGER NOT NULL DEFAULT 0,      -- blocked attempts
    tags TEXT NOT NULL DEFAULT '',                 -- comma-separated, normalized in Go
    -- Link expiry: a TTL or a view quota, never both. Enforced lazily on
    -- access (no cron); expiring flips the file private and clears both.
    expires_at TIMESTAMP,
    max_views INTEGER,
    view_count INTEGER NOT NULL DEFAULT 0,         -- only code-based views count
    -- How html_content is served: 'html' verbatim, 'markdown' rendered to
    -- HTML, 'txt' as escaped preformatted text. Fixed at creation.
    kind TEXT NOT NULL DEFAULT 'html',
    code_success_count INTEGER NOT NULL DEFAULT 0, -- viewed with a correct access code
    -- Owner. No DEFAULT on purpose: every caller supplies it, and a silent
    -- fallback would mis-attribute a file rather than fail loudly.
    user_id INTEGER NOT NULL
);

-- Serves the dashboard's default listing (active files, newest first).
CREATE INDEX idx_files_list ON files (created_at) WHERE deleted_at IS NULL;

-- Login sessions, in the database rather than in memory so logins survive
-- restarts. Expired rows are swept opportunistically on login; GetValidSession
-- also filters on expires_at, so an expired token is never valid meanwhile.
CREATE TABLE sessions (
    token TEXT PRIMARY KEY,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    user_id INTEGER NOT NULL
);

CREATE INDEX idx_sessions_expires_at ON sessions (expires_at);
