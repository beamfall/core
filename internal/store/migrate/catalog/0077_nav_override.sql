-- Navigation Manifest + Layout operator overrides (WEB-5J / ADR-0061 §Layout editor).
-- nav_override holds the current override for a given scope; nav_override_history is
-- the audit log of prior values (trimmed to the last 50 per scope to avoid unbounded growth).
--
-- scope_id is '' (empty string) for server-level scope and the profile_id value for
-- per-Profile scope. This avoids NULL in the composite primary key (SQLite disallows
-- expression columns in PRIMARY KEY declarations).

CREATE TABLE IF NOT EXISTS nav_override (
  scope      TEXT NOT NULL DEFAULT 'server', -- 'server' | 'profile'
  scope_id   TEXT NOT NULL DEFAULT '',        -- '' for server-level; profile_id for per-Profile
  data       TEXT NOT NULL DEFAULT '{}',      -- JSON NavOverrideData blob
  updated_at TEXT NOT NULL,
  PRIMARY KEY (scope, scope_id)
);

CREATE TABLE IF NOT EXISTS nav_override_history (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  scope      TEXT NOT NULL,
  scope_id   TEXT NOT NULL DEFAULT '',    -- '' for server-level; profile_id for per-Profile
  data       TEXT NOT NULL,              -- snapshot of data BEFORE the change
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_nav_override_history_scope
  ON nav_override_history(scope, scope_id, id DESC);
