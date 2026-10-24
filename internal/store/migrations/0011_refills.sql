-- Refills: Resets a User holds until they redeem one or it expires
-- (ADR 0010). A redeemed Refill is recorded as a Reset in quota_remedies.
CREATE TABLE refills (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL, -- Unix time; redeemable up to and including it
    redeemed_at INTEGER
);
CREATE INDEX refills_user ON refills (user_id, expires_at);

-- What a new-User Invite presets: the Quota, the running-out setting, and
-- Refills, whose lifetime counts from registration.
ALTER TABLE invites ADD COLUMN quota INTEGER;     -- NULL is unlimited
ALTER TABLE invites ADD COLUMN slow_rate INTEGER; -- NULL blocks
ALTER TABLE invites ADD COLUMN refills INTEGER NOT NULL DEFAULT 0;
ALTER TABLE invites ADD COLUMN refill_ttl INTEGER NOT NULL DEFAULT 0; -- seconds
