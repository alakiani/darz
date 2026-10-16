-- Alerts Alertmanager handed over and no Exit has relayed to Telegram yet
-- (ADR 0006). One that failed at least once is on the panel's banner.
CREATE TABLE alert_outbox (
    id          INTEGER PRIMARY KEY,
    text        TEXT NOT NULL,
    received_at INTEGER NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    last_error  TEXT NOT NULL DEFAULT ''
);

-- Nightly backups (ADR 0001): every attempt, the last few kept.
CREATE TABLE backups (
    id         INTEGER PRIMARY KEY,
    at         INTEGER NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0, -- encrypted; 0 if it failed
    error      TEXT NOT NULL DEFAULT ''    -- '' if it succeeded
);
