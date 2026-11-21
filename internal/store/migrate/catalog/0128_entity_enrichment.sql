-- Per-entity record of plugin entity enrichment checks (CORE-2884). The
-- background enrichment job looks each entity up once: a matched entity is
-- not checked again, an unmatched one is retried after its check ages out.
-- Rows go with their entity.
CREATE TABLE entity_enrichment (
  entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  enrichment TEXT NOT NULL,
  checked_at_ms INTEGER NOT NULL,
  matched INTEGER NOT NULL CHECK (matched IN (0, 1)),
  PRIMARY KEY (entity_id, enrichment)
);
