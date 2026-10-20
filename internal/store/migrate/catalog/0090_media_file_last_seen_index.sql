-- CAUDIT-21 / PR-4: large-library full scans mark files missing by
-- last_seen_at_ms after a completed walk. Keep the post-scan UPDATE off a full
-- media_file table scan for large libraries.
CREATE INDEX IF NOT EXISTS media_file_last_seen_live_idx
ON media_file(last_seen_at_ms)
WHERE missing_at_ms IS NULL;
