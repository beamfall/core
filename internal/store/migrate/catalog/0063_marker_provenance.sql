-- Skip-segment provenance (ENR-5 / ADR-0048 Decision 5): a skip-segment Marker
-- now records WHERE it came from — `manual` (user-authored/edited) vs `detected`
-- (Core's byte-level detector, ADR-0048 Decision 3/4). Detected segments also
-- record their method and a confidence score so the auto-apply threshold
-- (Decision 5) and idempotent re-detection ("replace only my own prior detected
-- output, never a manual one") have something to key on. Manual always wins:
-- the store rejects a `detected` write that would overwrite a `manual` row.
--
-- provenance is a closed two-value vocabulary (manual | detected); NOT NULL with
-- a DEFAULT so the existing marker rows (all user-authored) backfill to 'manual',
-- which is exactly their truth and the value that locks them from detection.
-- method is '' for manual segments and one of the Decision-4 method names for
-- detected ones (e.g. chapter-import | fingerprint). confidence is 0 for manual
-- (a hand-authored segment is certain by definition) and the detector's [0,1]
-- score for detected ones.
ALTER TABLE marker ADD COLUMN provenance TEXT NOT NULL DEFAULT 'manual'
  CHECK (provenance IN ('manual', 'detected'));
ALTER TABLE marker ADD COLUMN method TEXT NOT NULL DEFAULT '';
ALTER TABLE marker ADD COLUMN confidence REAL NOT NULL DEFAULT 0;

-- Suppression lock (ADR-0048 Decision 5: "deleting one suppresses that
-- (Media, kind) so re-detection won't resurrect it ... the suppression is
-- reversible"). A suppressed (media, kind) has NO marker row — it was deleted —
-- so the lock cannot ride a marker column; it lives in its own keyed table. A
-- future detector consults this table and skips a suppressed (media, kind);
-- "reset to auto-detection" deletes the row and hands the pair back. ON DELETE
-- CASCADE keeps it clean when the Media is purged. segment_kind is constrained
-- to the narrowed ADR-0048 Decision 1 vocabulary — only the auto-skippable kinds
-- can be detected, so only they can be suppressed.
CREATE TABLE IF NOT EXISTS marker_suppression (
  media_id      TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  segment_kind  TEXT NOT NULL CHECK (segment_kind IN ('intro', 'recap', 'credits')),
  created_at_ms INTEGER NOT NULL,
  PRIMARY KEY (media_id, segment_kind)
);
