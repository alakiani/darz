-- Users and Devices, as much as the data plane needs (ADR 0003). Session 6
-- adds credentials, Invites and caps.
CREATE TABLE users (
    id           TEXT PRIMARY KEY,
    handle       TEXT NOT NULL UNIQUE,
    suspended_at INTEGER,
    created_at   INTEGER NOT NULL
);

CREATE TABLE devices (
    id         TEXT PRIMARY KEY,
    user_id    TEXT REFERENCES users(id) ON DELETE CASCADE, -- NULL for the probe Device
    name       TEXT NOT NULL,
    public_key TEXT UNIQUE,            -- erased on revoke
    address    TEXT NOT NULL UNIQUE,   -- its /32 in 10.77.0.0/16, never reused while the row exists
    created_at INTEGER NOT NULL,
    revoked_at INTEGER
);

-- Destination Sets: the feed's last good content, plus Admin additions,
-- minus Admin removals. Content is stored by hash in set_blobs.
CREATE TABLE destination_sets (
    id           INTEGER PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    feed         TEXT NOT NULL DEFAULT '',   -- '' or the feed that fills it, such as 'ir'
    feed_content TEXT NOT NULL DEFAULT '[]', -- JSON prefixes: the last good feed update
    additions    TEXT NOT NULL DEFAULT '[]',
    removals     TEXT NOT NULL DEFAULT '[]',
    content_hash TEXT NOT NULL DEFAULT '',
    feed_updated_at INTEGER
);

CREATE TABLE set_blobs (
    hash TEXT PRIMARY KEY,
    body TEXT NOT NULL                       -- one prefix per line
);

CREATE TABLE rules (
    id            INTEGER PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    priority      INTEGER NOT NULL UNIQUE,   -- lowest wins
    set_id        INTEGER NOT NULL REFERENCES destination_sets(id),
    required_tags TEXT NOT NULL,             -- JSON array; "!IR" negates
    fallback      TEXT NOT NULL CHECK (fallback IN ('next', 'drop')),
    builtin       INTEGER NOT NULL DEFAULT 0 -- the default Rule cannot be deleted or changed
);

-- ADR 0004's two Rules.
INSERT INTO destination_sets (id, name, feed) VALUES (1, 'Iran', 'ir');
INSERT INTO destination_sets (id, name, additions) VALUES (2, 'Everything', '["0.0.0.0/0"]');
INSERT INTO rules (id, name, priority, set_id, required_tags, fallback, builtin)
    VALUES (1, 'IR', 100, 1, '["IR"]', 'next', 0);
INSERT INTO rules (id, name, priority, set_id, required_tags, fallback, builtin)
    VALUES (2, 'Default', 1000000, 2, '["!IR"]', 'drop', 1);
