-- CORE-3511 (CORE-0706): track retry count and next-attempt backoff for failed
-- post-commit artwork fetches so transient failures recover on subsequent
-- replays instead of completing permanently on the first failed fetch.
ALTER TABLE media_artwork_action ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_artwork_action ADD COLUMN next_attempt_at_ms INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS media_artwork_action_due ON media_artwork_action(state, next_attempt_at_ms, created_at_ms) WHERE state = 'pending';
