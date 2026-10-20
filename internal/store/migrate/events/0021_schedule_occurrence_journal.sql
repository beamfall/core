-- AUDITFIX-SCHEDULER-DURABILITY — durable schedule occurrence journal.
--
-- Audit finding: partial-enqueue progress for a due schedule lived only in
-- Service memory, so a crash mid-occurrence could duplicate members (rebuild +
-- re-enqueue on restart) or omit them (completion raced ahead of publication).
-- This journal records one row per due occurrence — identified by the
-- deterministic due instant, never by wall-clock arrival — plus one row per
-- member job the occurrence must publish. A member's job row and its journal
-- mark are written in ONE events-DB transaction, so every crash boundary lands
-- on a state the resume path reads back exactly: a member is either published
-- (job_id set) or pending, never half-done.
--
-- The tables live in the events DB because the job table lives here: the
-- member-publication mark MUST commit atomically with the job insert. The
-- schedules table (catalog DB) keeps only the monotonic completion cursor.
-- Off the entity-query hot path (job/scheduler plumbing, not catalog).
CREATE TABLE IF NOT EXISTS schedule_occurrence (
  schedule_id TEXT NOT NULL,
  due_ms INTEGER NOT NULL,
  started_at_ms INTEGER NOT NULL,
  completed_at_ms INTEGER,
  PRIMARY KEY (schedule_id, due_ms)
);

CREATE TABLE IF NOT EXISTS schedule_occurrence_member (
  schedule_id TEXT NOT NULL,
  due_ms INTEGER NOT NULL,
  ordinal INTEGER NOT NULL,
  job_kind TEXT NOT NULL,
  job_class TEXT NOT NULL,
  job_priority INTEGER NOT NULL DEFAULT 0,
  job_dedupe_key TEXT,
  job_payload_json TEXT NOT NULL,
  job_created_at_ms INTEGER NOT NULL,
  job_run_after_ms INTEGER NOT NULL,
  job_id TEXT,
  enqueued_at_ms INTEGER,
  PRIMARY KEY (schedule_id, due_ms, ordinal),
  FOREIGN KEY (schedule_id, due_ms)
    REFERENCES schedule_occurrence(schedule_id, due_ms) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS schedule_occurrence_open_idx
ON schedule_occurrence(schedule_id, due_ms)
WHERE completed_at_ms IS NULL;
