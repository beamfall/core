CREATE TABLE IF NOT EXISTS library_roots (
  library_id TEXT NOT NULL REFERENCES library(id) ON DELETE CASCADE,
  root TEXT NOT NULL,
  PRIMARY KEY (library_id, root)
);

CREATE TABLE IF NOT EXISTS schedules (
  id TEXT PRIMARY KEY,
  job_kind TEXT NOT NULL,
  trigger_kind TEXT NOT NULL CHECK (trigger_kind IN ('every', 'daily', 'weekly')),
  every_n INTEGER,
  every_unit TEXT CHECK (every_unit IS NULL OR every_unit IN ('minutes', 'hours')),
  weekday INTEGER,
  hour INTEGER,
  minute INTEGER,
  enabled INTEGER NOT NULL DEFAULT 1,
  orphaned INTEGER NOT NULL DEFAULT 0,
  last_completed_enqueue_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS schedules_job_kind_idx ON schedules(job_kind, id);
