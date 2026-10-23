-- Endpoint Slots (ADR 0005): the Bridge each slot is assigned to, and the
-- address its DNS record was last set to. addr is '' until ArvanCloud
-- accepts the update, so a failed update is retried.
CREATE TABLE endpoint_slots (
    name       TEXT PRIMARY KEY,           -- such as a.wayfinder.ir
    node_id    TEXT NOT NULL,
    addr       TEXT NOT NULL DEFAULT '',
    changed_at INTEGER NOT NULL,           -- when node_id last changed
    repoints   INTEGER NOT NULL DEFAULT 0  -- moves off a failed Bridge
);
