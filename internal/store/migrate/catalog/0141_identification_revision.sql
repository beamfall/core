-- CORE-0706: every committed identification of a shared-catalog Media appends
-- one immutable revision row in the same transaction as the metadata, field and
-- provenance writes. request_id is the idempotency receipt: a replay of the same
-- request with the same content (request_digest) reports the committed revision
-- instead of applying twice, and the same request with different content is
-- refused. kind is 'identify' or 'fix-match'; detail_json carries the Fix Match
-- audit (cleared locks, ordinal disposition, pin). Rows are never updated and
-- leave only with their Media (ON DELETE CASCADE). The Encrypted Library mirror
-- is encrypted-library 0018.
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
