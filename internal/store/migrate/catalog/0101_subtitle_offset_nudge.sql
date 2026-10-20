-- SUBSYS-13 (ADR-0194 §4): a viewer's in-player subtitle offset nudge persists
-- silently, PER PROFILE, keyed on the subtitle-track FINGERPRINT — never on the
-- media item.
--
-- The fingerprint is a content hash of the track BYTES (SUBSYS-12,
-- store.SubtitleTrackFingerprint), which is load-bearing against ADR-0194 §5: a
-- companion co-rename fires on every routine video rename, so a path-keyed nudge
-- would orphan on each rename. Byte-hash keying survives renames and remuxes
-- (bytes unchanged) and, by design, invalidates structurally when a permanent
-- timing fix rewrites the bytes — the new hash simply stops matching at lookup,
-- so there is no double-shift and no invalidation event to deliver.
--
-- offset_ms is signed: negative pulls cues earlier, positive pushes them later.
-- No media id is stored, so the row carries no content reference and reveal
-- rules do not apply; orphaned rows (a track whose bytes no longer exist) are
-- reaped by a cheap age-based prune rather than by reference tracking.
CREATE TABLE subtitle_offset_nudge (
  profile_id        TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  track_fingerprint TEXT NOT NULL,
  offset_ms         INTEGER NOT NULL,
  updated_at_ms     INTEGER NOT NULL,
  PRIMARY KEY (profile_id, track_fingerprint)
);

-- The prune sweep scans by recency across all profiles.
CREATE INDEX subtitle_offset_nudge_updated_idx ON subtitle_offset_nudge(updated_at_ms);
