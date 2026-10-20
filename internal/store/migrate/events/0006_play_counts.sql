ALTER TABLE playback_progress ADD COLUMN play_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE playback_progress ADD COLUMN last_played_at_ms INTEGER;
