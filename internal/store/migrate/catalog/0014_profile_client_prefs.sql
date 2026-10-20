-- Per-Profile client display preferences (pre-freeze ask, competitor-gap
-- A23): server-stored roaming for client-owned display prefs (subtitle
-- styling, theme toggles). Keys are client-namespaced; values are opaque —
-- the server never interprets them, and by convention they carry no content
-- references, so reveal rules do not apply.
CREATE TABLE profile_client_prefs (
  profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  updated_at_ms INTEGER NOT NULL,
  PRIMARY KEY (profile_id, key)
);
