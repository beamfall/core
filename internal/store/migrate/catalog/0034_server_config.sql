-- Server runtime config (ADR-0013 §5): the Console-owned operational settings
-- that live in the database, distinct from the bootstrap, file/env-shaped
-- config (ports, data dir, admin bootstrap, TLS) read by internal/config at
-- process start. A small key-value table over a closed, server-validated key
-- vocabulary: the Console reads and writes named values (background-worker
-- budget, hardware-transcode policy), the server validates each key on write,
-- so a row can never carry an unknown key. Absent key = the server default; a
-- present row overrides it. Values are stored as TEXT (the typed read/write
-- layer parses each per its key) to keep the table key-agnostic.
CREATE TABLE IF NOT EXISTS server_config (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at_ms INTEGER NOT NULL DEFAULT 0
);
