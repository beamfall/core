-- CORE-0706: identification of an Encrypted Library's Media commits on the
-- Media's own plane. media_metadata_provenance mirrors catalog 0113 + 0119,
-- media_operator_ordinal mirrors catalog 0132 (actor_profile_id has no foreign
-- key: Profiles live in the shared catalog, and a Profile deletion clears it
-- here), and media_identification_revision mirrors catalog 0140, so one SQL
-- text serves both planes. No candidate ID, title or scraper choice for a
-- private Media reaches the shared catalog.
CREATE TABLE media_metadata_provenance (
  media_id TEXT PRIMARY KEY REFERENCES media(id) ON DELETE CASCADE,
  plugin_id TEXT NOT NULL,
  scraper_id TEXT NOT NULL,
  updated_at_ms INTEGER NOT NULL,
  candidate_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE media_operator_ordinal (
  media_id TEXT NOT NULL PRIMARY KEY REFERENCES media(id) ON DELETE CASCADE,
  sequence TEXT NOT NULL CHECK (length(sequence) <= 32),
  ordinal_index INTEGER NOT NULL CHECK (ordinal_index BETWEEN 0 AND 2147483647),
  set_at_ms INTEGER NOT NULL,
  actor_profile_id TEXT
);
CREATE TABLE media_identification_revision (
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL CHECK (revision > 0),
  request_id TEXT NOT NULL CHECK (request_id <> ''),
  request_digest TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('identify', 'fix-match')),
  plugin_id TEXT NOT NULL,
  scraper_id TEXT NOT NULL,
  candidate_id TEXT NOT NULL DEFAULT '',
  actor_profile_id TEXT,
  detail_json TEXT NOT NULL DEFAULT '{}',
  applied_at_ms INTEGER NOT NULL,
  PRIMARY KEY (media_id, revision),
  UNIQUE (media_id, request_id)
);
CREATE TRIGGER media_identification_revision_immutable
BEFORE UPDATE ON media_identification_revision
BEGIN
  SELECT RAISE(ABORT, 'identification revisions are immutable');
END;
