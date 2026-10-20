-- First-class, domain-agnostic Tags for Manage curation (WEB-5D / ADR-0061).
-- Existing marker_tag rows remain Marker display tags; marker_core_tag is the
-- first-class Tag assignment table so Marker label fallback semantics do not
-- change under this migration.
CREATE TABLE tag (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  parent_tag_id TEXT REFERENCES tag(id) ON DELETE SET NULL,
  trashed_at_ms INTEGER,
  created_at_ms INTEGER NOT NULL,
  updated_at_ms INTEGER NOT NULL,
  CHECK (id <> parent_tag_id)
);

CREATE UNIQUE INDEX tag_live_name_uq
  ON tag(lower(name))
  WHERE trashed_at_ms IS NULL;
CREATE INDEX tag_parent_idx
  ON tag(parent_tag_id)
  WHERE parent_tag_id IS NOT NULL AND trashed_at_ms IS NULL;
CREATE INDEX tag_trashed_idx
  ON tag(trashed_at_ms)
  WHERE trashed_at_ms IS NOT NULL;

CREATE TABLE media_tag (
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  tag_id TEXT NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  PRIMARY KEY (media_id, tag_id)
);
CREATE INDEX media_tag_tag_idx ON media_tag(tag_id);

CREATE TABLE entity_tag (
  entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  tag_id TEXT NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  PRIMARY KEY (entity_id, tag_id)
);
CREATE INDEX entity_tag_tag_idx ON entity_tag(tag_id);

CREATE TABLE marker_core_tag (
  marker_id TEXT NOT NULL REFERENCES marker(id) ON DELETE CASCADE,
  tag_id TEXT NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  PRIMARY KEY (marker_id, tag_id)
);
CREATE INDEX marker_core_tag_tag_idx ON marker_core_tag(tag_id);
