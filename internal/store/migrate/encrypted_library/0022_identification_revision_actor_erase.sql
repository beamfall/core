-- CORE-3511 (CORE-0706): Encrypted Library mirror of catalog 0144 allowing
-- Profile deletion and footprint erasure to null actor_profile_id while
-- keeping all identification revision data immutable.
DROP TRIGGER IF EXISTS media_identification_revision_immutable;
CREATE TRIGGER media_identification_revision_immutable
BEFORE UPDATE ON media_identification_revision
WHEN NOT (
  NEW.actor_profile_id IS NULL
  AND NEW.media_id = OLD.media_id
  AND NEW.revision = OLD.revision
  AND NEW.request_id = OLD.request_id
  AND NEW.request_digest = OLD.request_digest
  AND NEW.kind = OLD.kind
  AND NEW.plugin_id = OLD.plugin_id
  AND NEW.scraper_id = OLD.scraper_id
  AND NEW.candidate_id = OLD.candidate_id
  AND NEW.detail_json = OLD.detail_json
  AND NEW.applied_at_ms = OLD.applied_at_ms
)
BEGIN
  SELECT RAISE(ABORT, 'identification revisions are immutable');
END;
