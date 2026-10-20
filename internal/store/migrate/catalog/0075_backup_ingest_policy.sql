-- ENC-4: per-Profile backup ingest policy. This is control-plane state only:
-- it carries quota/pause configuration, never uploaded Media bytes or
-- encryption keys. Uploaded byte identity is registered through media_file in
-- the target Library's existing catalog plane.
CREATE TABLE backup_ingest_policy (
  profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  library_id TEXT NOT NULL REFERENCES library(id) ON DELETE CASCADE,
  quota_bytes INTEGER NOT NULL DEFAULT 0,
  paused INTEGER NOT NULL DEFAULT 0,
  updated_at_ms INTEGER NOT NULL,
  PRIMARY KEY (profile_id, library_id)
);
