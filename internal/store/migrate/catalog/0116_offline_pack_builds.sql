-- Offline Pack builds in progress (CORE-1743). A build queued under a
-- pre-assigned pack ID has no offline_pack row until the job seals it, so this
-- table carries the pending row clients list and retry: its state, coarse
-- progress and a closed failure-reason code. The row is deleted in the same
-- transaction that inserts the sealed pack. job_payload is the build request the
-- retry path re-queues under the caller's current scope.
CREATE TABLE IF NOT EXISTS offline_pack_build (
  pack_id        TEXT    NOT NULL PRIMARY KEY,
  profile_id     TEXT    NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  device_id      TEXT    NOT NULL DEFAULT '',
  target_kind    TEXT    NOT NULL,
  target_ref     TEXT    NOT NULL DEFAULT '',
  quality        TEXT    NOT NULL DEFAULT '',
  state          TEXT    NOT NULL CHECK (state IN ('building', 'failed')),
  progress       REAL    NOT NULL DEFAULT 0 CHECK (progress >= 0 AND progress <= 1),
  failure_reason TEXT    NOT NULL DEFAULT '',
  job_payload    BLOB    NOT NULL,
  created_at_ms  INTEGER NOT NULL CHECK (created_at_ms >= 0),
  updated_at_ms  INTEGER NOT NULL CHECK (updated_at_ms >= created_at_ms)
);

CREATE INDEX IF NOT EXISTS offline_pack_build_profile_idx
  ON offline_pack_build(profile_id, updated_at_ms);
