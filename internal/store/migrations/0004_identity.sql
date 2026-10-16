-- Credentials and caps for Users (ADR 0003). No email, phone or name:
-- a handle and credentials only (ADR 0001).
ALTER TABLE users ADD COLUMN password_hash TEXT;
ALTER TABLE users ADD COLUMN totp_secret TEXT;        -- set once confirmed
ALTER TABLE users ADD COLUMN totp_pending TEXT;       -- offered, not yet confirmed
ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN device_cap INTEGER NOT NULL DEFAULT 3;

-- Single-use Invites, stored hashed. A recovery Invite names the User whose
-- credentials it replaces.
CREATE TABLE invites (
    hash       BLOB PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('new', 'recovery')),
    user_id    TEXT REFERENCES users(id) ON DELETE CASCADE,
    is_admin   INTEGER NOT NULL DEFAULT 0,
    device_cap INTEGER,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
);

-- Panel sessions, stored hashed. mfa is set when the session proved a
-- second factor, which Admin actions require.
CREATE TABLE sessions (
    hash       BLOB PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mfa        INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);
