-- MIG-3C / ADR-0037 generic item-state import ledger.
--
-- Podcast item-state import needs replay for feed items that are not yet
-- downloaded. The safe key is the existing feed scope (subscription_id) plus
-- the source item GUID; the table deliberately does NOT persist raw feed URLs
-- or auth headers, so it does not duplicate private-feed secrets beyond the
-- existing subscription row.
--
-- applied_* is a current-state marker, not history: a re-import refreshes the
-- desired state and clears applied_* so replay can advance Beamfall's additive
-- playback/favorite projections again without using GUID-only matching.
CREATE TABLE item_state_import (
  profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  subscription_id TEXT NOT NULL REFERENCES subscription(id) ON DELETE CASCADE,
  source TEXT NOT NULL DEFAULT '',
  item_guid TEXT NOT NULL,
  played INTEGER NOT NULL DEFAULT 0,
  has_progress INTEGER NOT NULL DEFAULT 0,
  progress REAL NOT NULL DEFAULT 0 CHECK (progress >= 0 AND progress <= 1),
  starred INTEGER NOT NULL DEFAULT 0,
  imported_at_ms INTEGER NOT NULL,
  applied_at_ms INTEGER NOT NULL DEFAULT 0,
  applied_media_id TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (profile_id, subscription_id, item_guid)
);

CREATE INDEX item_state_import_pending_idx
  ON item_state_import(applied_at_ms, subscription_id, item_guid);
