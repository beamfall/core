-- Soft-delete for Entities (FEAT-6 / ADR-0005 §3 entity merge): the loser of a
-- merge is TRASHED, never hard-deleted (hard invariant #6 — recoverable). The
-- same trashed_at_ms epoch-ms convention used on Media (0007_media_trash.sql)
-- and Library (0040_library_trashed.sql). A trashed Entity is hidden from every
-- reveal-filtered read and listing but its rows (fields, relations) remain
-- intact until an explicit restore (UndoBatch) or a future purge.
ALTER TABLE entity ADD COLUMN trashed_at_ms INTEGER;

CREATE INDEX IF NOT EXISTS idx_entity_trashed ON entity (trashed_at_ms)
  WHERE trashed_at_ms IS NOT NULL;
