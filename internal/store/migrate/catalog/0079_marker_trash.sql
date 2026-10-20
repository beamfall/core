-- WEB-5E / ADR-0061 marker restore substrate: Markers are content-level
-- curation rows and the Manage surface must be able to restore an accidental
-- delete. Preserve marker_tag rows and hide soft-deleted markers from normal
-- timeline reads until restored.
ALTER TABLE marker ADD COLUMN trashed_at_ms INTEGER;

CREATE INDEX IF NOT EXISTS idx_marker_trashed ON marker (media_id, trashed_at_ms)
  WHERE trashed_at_ms IS NOT NULL;
