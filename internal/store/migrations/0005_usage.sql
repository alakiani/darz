-- Metering (ADR 0003): bytes per Device per Exit, hourly for 7 days, then
-- daily for 90 days, then monthly per Device until the User is deleted.
-- Nothing finer, and no destinations.

-- Hourly rows are kept per reporting Bridge, so an upload replaces exactly
-- its own row and resending is harmless (ADR 0002).
CREATE TABLE usage_hourly (
    node_id   TEXT NOT NULL,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    exit_id   TEXT NOT NULL,
    hour      INTEGER NOT NULL,
    up        INTEGER NOT NULL,
    down      INTEGER NOT NULL,
    PRIMARY KEY (node_id, device_id, exit_id, hour)
);

CREATE TABLE usage_daily (
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    exit_id   TEXT NOT NULL,
    day       INTEGER NOT NULL, -- Unix time of the UTC day's start
    up        INTEGER NOT NULL,
    down      INTEGER NOT NULL,
    PRIMARY KEY (device_id, exit_id, day)
);

CREATE TABLE usage_monthly (
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    month     TEXT NOT NULL, -- YYYY-MM, UTC
    up        INTEGER NOT NULL,
    down      INTEGER NOT NULL,
    PRIMARY KEY (device_id, month)
);
