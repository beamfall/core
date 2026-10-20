CREATE TABLE IF NOT EXISTS job (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  class TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  dedupe_key TEXT,
  priority INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  created_at_ms INTEGER NOT NULL,
  updated_at_ms INTEGER NOT NULL,
  available_at_ms INTEGER NOT NULL,
  started_at_ms INTEGER,
  finished_at_ms INTEGER,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  lease_owner TEXT,
  lease_expires_at_ms INTEGER,
  cancel_requested_at_ms INTEGER,
  progress_current INTEGER NOT NULL DEFAULT 0,
  progress_total INTEGER NOT NULL DEFAULT 0,
  progress_unit TEXT,
  progress_message TEXT,
  last_error TEXT
);

CREATE TABLE IF NOT EXISTS job_checkpoint (
  job_id TEXT PRIMARY KEY REFERENCES job(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  checkpoint_json TEXT NOT NULL,
  updated_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS job_event (
  id INTEGER PRIMARY KEY,
  job_id TEXT NOT NULL REFERENCES job(id) ON DELETE CASCADE,
  occurred_at_ms INTEGER NOT NULL,
  type TEXT NOT NULL,
  payload_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS event_outbox (
  seq INTEGER PRIMARY KEY,
  occurred_at_ms INTEGER NOT NULL,
  kind TEXT NOT NULL,
  aggregate_type TEXT NOT NULL,
  aggregate_id TEXT NOT NULL,
  payload_json TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS job_active_dedupe_idx
ON job(kind, dedupe_key)
WHERE dedupe_key IS NOT NULL AND status IN ('queued', 'running', 'yielded');

CREATE INDEX IF NOT EXISTS job_runnable_idx
ON job(class, status, priority DESC, available_at_ms, created_at_ms);

CREATE INDEX IF NOT EXISTS job_status_updated_idx
ON job(status, updated_at_ms DESC);

CREATE INDEX IF NOT EXISTS job_class_status_idx
ON job(class, status, updated_at_ms DESC);

CREATE INDEX IF NOT EXISTS job_expired_lease_idx
ON job(lease_expires_at_ms)
WHERE status = 'running';

CREATE INDEX IF NOT EXISTS job_event_job_time_idx
ON job_event(job_id, occurred_at_ms, id);
