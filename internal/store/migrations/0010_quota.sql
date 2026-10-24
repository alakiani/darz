-- Each User's Quota and what happens when they run out (ADR 0010). Every
-- User starts unlimited, and running out blocks unless a slow rate is set.
ALTER TABLE users ADD COLUMN quota INTEGER;     -- bytes per Quota Period; NULL is unlimited
ALTER TABLE users ADD COLUMN slow_rate INTEGER; -- bytes per second once out; NULL blocks

-- Top-ups and Resets. Each adds bytes to what a User may still use in one
-- Quota Period and never changes recorded Usage; erased with the User.
CREATE TABLE quota_remedies (
    id           INTEGER PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period_start INTEGER NOT NULL, -- Unix time, 00:00 UTC: the period it adds to
    kind         TEXT NOT NULL CHECK (kind IN ('top-up', 'reset')),
    bytes        INTEGER NOT NULL,
    created_at   INTEGER NOT NULL
);
CREATE INDEX quota_remedies_user ON quota_remedies (user_id, period_start);
