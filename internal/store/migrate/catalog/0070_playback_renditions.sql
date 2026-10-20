-- Precomputed Playback Renditions (ADR-0057): disposable, rebuildable
-- playback cache objects, separate from user-owned media_file Versions.
CREATE TABLE playback_rendition (
  id TEXT NOT NULL PRIMARY KEY,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  content_fingerprint TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('hls')),
  status TEXT NOT NULL CHECK (status IN ('generating', 'ready', 'failed', 'evicted')),
  rung_id TEXT NOT NULL,
  codec TEXT NOT NULL,
  height INTEGER NOT NULL DEFAULT 0,
  bandwidth INTEGER NOT NULL DEFAULT 0,
  path TEXT NOT NULL,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  created_at_ms INTEGER NOT NULL,
  last_used_at_ms INTEGER NOT NULL
);

CREATE INDEX playback_rendition_media_ready_idx
ON playback_rendition(media_id, status, height DESC, bandwidth DESC);

CREATE INDEX playback_rendition_eviction_idx
ON playback_rendition(status, last_used_at_ms, created_at_ms);
