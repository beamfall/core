-- Plugin activation override (ADR-0013 §5, ADR-0007 §4): plugin activation is
-- Console-owned operational state in the database. The in-process plugin
-- Registry (internal/plugin) is the set of bundles loaded and validated at
-- boot — i.e. which plugins are AVAILABLE. This table records the Console's
-- per-plugin active/inactive OVERRIDE for those available plugins. A plugin
-- with no row here is active by default (the historical behavior — loading a
-- bundle activated it); a row with active = 0 deactivates it. plugin_id is the
-- registry bundle ID; the server validates membership against the registry on
-- write, so a row can never name an unknown plugin.
CREATE TABLE IF NOT EXISTS plugin_activation (
  plugin_id TEXT PRIMARY KEY,
  active INTEGER NOT NULL DEFAULT 1,
  updated_at_ms INTEGER NOT NULL DEFAULT 0
);
