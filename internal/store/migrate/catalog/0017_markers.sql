-- Markers (ADR-0027 D2 §2, ADR-0005): a named point or range on one Media
-- item's canonical timeline. start_seconds alone is a point marker; an
-- end_seconds makes it a range (a range's thumbnail is its start time,
-- ADR-0033). segment_kind is the closed skip-segment vocabulary — five
-- shape-named kinds clients compile in for skip-intro-style affordances —
-- distinct from free-form marker tags, which live in marker_tag. REAL
-- seconds: markers are frame-accurate timeline points (thumbnail grabs),
-- unlike whole-second playback progress.
CREATE TABLE marker (
  id TEXT PRIMARY KEY,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  start_seconds REAL NOT NULL CHECK (start_seconds >= 0),
  end_seconds REAL CHECK (end_seconds > start_seconds),
  segment_kind TEXT CHECK (segment_kind IN ('intro', 'credits', 'recap', 'preview', 'commercial')),
  created_at_ms INTEGER NOT NULL,
  updated_at_ms INTEGER NOT NULL
);
CREATE INDEX marker_media_start_idx ON marker(media_id, start_seconds);

-- Free-form marker tags (ADR-0027 D2 §2: Markers gain optional core Tags).
-- No "primary tag" slot — an untitled marker displays its first tag.
CREATE TABLE marker_tag (
  marker_id TEXT NOT NULL REFERENCES marker(id) ON DELETE CASCADE,
  tag TEXT NOT NULL,
  PRIMARY KEY (marker_id, tag)
);
CREATE INDEX marker_tag_tag_idx ON marker_tag(tag);
