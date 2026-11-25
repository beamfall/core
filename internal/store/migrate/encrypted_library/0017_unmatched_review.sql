-- CORE-0706 (COREAI-PRIVATE-INGEST-PLANE-1, slice 2): Unmatched Records and
-- their scoped suppressions for an Encrypted Library live on that Library's
-- own plane. A record's source handle (a file path or external-id summary),
-- candidate title and suppression provenance name the target, so they never
-- reach the shared catalog. Shapes mirror catalog 0094 and 0102 so one SQL
-- text serves both planes. library_id has no foreign key: the library table
-- stays in the shared catalog, and every row here belongs to this Library.
CREATE TABLE unmatched_record (
  id TEXT NOT NULL PRIMARY KEY,
  origin TEXT NOT NULL,
  library_id TEXT,
  media_id TEXT REFERENCES media(id) ON DELETE SET NULL,
  import_run_id TEXT NOT NULL DEFAULT '',
  import_importer_id TEXT NOT NULL DEFAULT '',
  source_handle TEXT NOT NULL DEFAULT '',
  candidate_id TEXT NOT NULL DEFAULT '',
  candidate_title TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 0,
  failure_reason TEXT NOT NULL DEFAULT '',
  queued_at_ms INTEGER NOT NULL,
  resolved_at_ms INTEGER,
  resolved_media_id TEXT REFERENCES media(id) ON DELETE SET NULL
);
CREATE INDEX unmatched_record_pending_idx
  ON unmatched_record (queued_at_ms)
  WHERE resolved_at_ms IS NULL;
CREATE INDEX unmatched_record_origin_idx ON unmatched_record (origin);
CREATE INDEX unmatched_record_media_idx ON unmatched_record (media_id);

CREATE TABLE unmatched_suppression (
  scope TEXT NOT NULL,
  scope_key TEXT NOT NULL,
  record_id TEXT,
  source_handle TEXT NOT NULL DEFAULT '',
  actor_profile_id TEXT,
  created_at_ms INTEGER NOT NULL,
  PRIMARY KEY (scope, scope_key)
);
CREATE INDEX unmatched_suppression_record_idx ON unmatched_suppression (record_id);
