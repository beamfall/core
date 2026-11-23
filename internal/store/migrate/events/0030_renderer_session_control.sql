-- CORE-3309 (ADR-0151 D1-D3, ADR-0240 D3): monotonic per-Renderer-Session
-- revision and idempotent command ledger for revision-checked session control.
-- Keyed by session_id / command_id in events.db so revision increments,
-- command ledger inserts and event_outbox writes commit in one transaction.
-- Off the entity-query hot path.
CREATE TABLE renderer_session_revision (
  session_id TEXT NOT NULL PRIMARY KEY,
  revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
  updated_at_ms INTEGER NOT NULL
);

CREATE TABLE renderer_session_command (
  command_id TEXT NOT NULL PRIMARY KEY,
  session_id TEXT NOT NULL,
  body_sha256 TEXT NOT NULL,
  status TEXT NOT NULL,
  command TEXT NOT NULL,
  revision INTEGER NOT NULL CHECK (revision >= 0),
  result_json TEXT NOT NULL,
  created_at_ms INTEGER NOT NULL
);

CREATE INDEX idx_renderer_session_command_created
  ON renderer_session_command (created_at_ms);
