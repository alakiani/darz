-- Times are Unix seconds, UTC.

CREATE TABLE nodes (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    roles       TEXT NOT NULL,          -- JSON array of roles
    tags        TEXT NOT NULL,          -- JSON array of tags
    cert_serial TEXT,                   -- hex serial of the current client certificate; NULL until enrolled
    created_at  INTEGER NOT NULL
);

-- One-time enrollment tokens, stored hashed. Minting a new one for a Node
-- deletes its unused ones.
CREATE TABLE enrollment_tokens (
    hash       BLOB PRIMARY KEY,
    node_id    TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
);

-- Scoped API tokens for non-interactive callers, stored hashed.
CREATE TABLE api_tokens (
    id         TEXT PRIMARY KEY,
    hash       BLOB NOT NULL UNIQUE,
    scope      TEXT NOT NULL,
    name       TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER
);

-- Each Node's desired state, one row per version.
CREATE TABLE snapshots (
    node_id    TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    version    INTEGER NOT NULL,
    hash       BLOB NOT NULL,
    body       BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    bad_at     INTEGER,                 -- set when the Node reported apply_failed for this version
    PRIMARY KEY (node_id, version)
);

-- The latest health report per Node, as JSON.
CREATE TABLE node_health (
    node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    body    TEXT NOT NULL
);
