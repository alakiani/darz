-- One total per User per closed Quota Period (ADR 0010), kept until the
-- User is deleted. Bytes are up plus down, revoked Devices included.
CREATE TABLE usage_periods (
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period_start INTEGER NOT NULL, -- Unix time, 00:00 UTC
    period_end   INTEGER NOT NULL,
    bytes        INTEGER NOT NULL,
    PRIMARY KEY (user_id, period_start)
);
