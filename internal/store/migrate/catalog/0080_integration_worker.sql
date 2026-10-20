-- PLUG-1 / ADR-0058 third-party Integration Worker runtime. Server-owned state
-- for installed worker artifacts, permission grants (with their bound Reveal
-- scope), and the per-plugin Update Source. All operator-facing operational
-- state; no domain vocabulary (invariants #1/#2/#3) and no secret material
-- (secrets live in the 0600 secret store, never here).

-- One row per plugin with an installed Integration Worker Artifact. The verified
-- artifact hash and signing key id are recorded at install/update so the Plugin
-- Surface can render what is running and an update transition is auditable.
CREATE TABLE IF NOT EXISTS integration_worker (
  plugin_id        TEXT PRIMARY KEY,
  version          TEXT NOT NULL DEFAULT '',
  artifact_sha256  TEXT NOT NULL DEFAULT '',
  signing_key_id   TEXT NOT NULL DEFAULT '',
  state            TEXT NOT NULL DEFAULT 'installed',
  last_error       TEXT NOT NULL DEFAULT '',
  updated_at_ms    INTEGER NOT NULL
);

-- One row per (plugin, permission) grant. approved gates whether a high-risk
-- Host API method works; a permission-expanding update flips affected grants
-- back to unapproved (re-approval). bound_profile_id / all_libraries carry the
-- read-catalog/event-subscribe Reveal scope binding and are MUTUALLY EXCLUSIVE:
-- the CHECK forbids a grant that sets both, which could bypass Reveal filtering.
-- (A Go validatePluginGrant enforces the same invariant before the write.)
CREATE TABLE IF NOT EXISTS plugin_grant (
  plugin_id        TEXT NOT NULL,
  kind             TEXT NOT NULL,
  approved         INTEGER NOT NULL DEFAULT 0,
  bound_profile_id TEXT NOT NULL DEFAULT '',
  all_libraries    INTEGER NOT NULL DEFAULT 0,
  updated_at_ms    INTEGER NOT NULL,
  PRIMARY KEY (plugin_id, kind),
  CHECK (NOT (bound_profile_id <> '' AND all_libraries = 1))
);

-- One row per plugin with an operator-configured Update Source (ADR-0058 — not a
-- marketplace). kind is 'url' or 'folder'; pin_kind is 'hash' or 'signing-key'.
CREATE TABLE IF NOT EXISTS plugin_update_source (
  plugin_id     TEXT PRIMARY KEY,
  kind          TEXT NOT NULL,
  location      TEXT NOT NULL,
  pin_kind      TEXT NOT NULL,
  pin           TEXT NOT NULL,
  updated_at_ms INTEGER NOT NULL
);
