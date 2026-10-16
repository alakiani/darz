-- Invites get an id an Admin can list and withdraw them by; the hash
-- stays the only way to redeem one.
ALTER TABLE invites ADD COLUMN id TEXT;
ALTER TABLE invites ADD COLUMN withdrawn_at INTEGER;
UPDATE invites SET id = 'inv_' || lower(hex(randomblob(8))) WHERE id IS NULL;
CREATE UNIQUE INDEX invites_id ON invites (id);

-- The feed's previous good content, for roll back, and how the last fetch
-- went, for the panel.
ALTER TABLE destination_sets ADD COLUMN feed_previous TEXT NOT NULL DEFAULT '[]';
ALTER TABLE destination_sets ADD COLUMN feed_last_attempt INTEGER;
ALTER TABLE destination_sets ADD COLUMN feed_last_error TEXT NOT NULL DEFAULT '';
