-- CORE-3511 (CORE-0706): Encrypted Library mirror of catalog 0143 for bounded
-- post-commit artwork fetch retry and next-attempt backoff.
ALTER TABLE media_artwork_action ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_artwork_action ADD COLUMN next_attempt_at_ms INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS media_artwork_action_due ON media_artwork_action(state, next_attempt_at_ms, created_at_ms) WHERE state = 'pending';
