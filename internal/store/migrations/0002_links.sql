-- The Node's Link public key, reported by its Agent (ADR 0007).
ALTER TABLE nodes ADD COLUMN link_public_key TEXT;

-- One Link per Node pair, derived by the Coordinator. node_a < node_b.
CREATE TABLE links (
    id       INTEGER PRIMARY KEY,
    node_a   TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    node_b   TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    disabled INTEGER NOT NULL DEFAULT 0,
    UNIQUE (node_a, node_b),
    CHECK (node_a < node_b)
);

-- A Link's candidate Transport instances. A replaced instance is retired,
-- never edited: its port and parameters never change.
CREATE TABLE link_instances (
    id          INTEGER PRIMARY KEY,
    link_id     INTEGER NOT NULL REFERENCES links(id) ON DELETE CASCADE,
    transport   TEXT NOT NULL,
    listener    TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    slot        INTEGER NOT NULL,       -- index of the instance's /31 in the mesh range
    listen_port INTEGER,                -- chosen by the listener; NULL until reported
    awg_params  TEXT,                   -- JSON, chosen by the listener for AmneziaWG
    created_at  INTEGER NOT NULL,
    down_since  INTEGER,                -- as reported by the initiating end
    retired_at  INTEGER
);
CREATE UNIQUE INDEX link_instances_live_slot ON link_instances (slot) WHERE retired_at IS NULL;

-- Global settings, such as the ordered candidate Transport list.
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
