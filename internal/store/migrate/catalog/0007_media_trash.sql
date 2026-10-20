ALTER TABLE media ADD COLUMN trashed_at_ms INTEGER;

CREATE INDEX IF NOT EXISTS idx_media_trashed ON media (trashed_at_ms)
  WHERE trashed_at_ms IS NOT NULL;
