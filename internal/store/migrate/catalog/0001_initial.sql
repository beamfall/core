CREATE TABLE IF NOT EXISTS library (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  plugin_id TEXT,
  is_restricted INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS profile (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS profile_library (
  profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  library_id TEXT NOT NULL REFERENCES library(id) ON DELETE CASCADE,
  position INTEGER NOT NULL,
  PRIMARY KEY (profile_id, library_id)
);

CREATE TABLE IF NOT EXISTS device (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  role TEXT NOT NULL,
  secret_hash TEXT NOT NULL,
  revoked INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS auth_session (
  id TEXT PRIMARY KEY,
  device_id TEXT NOT NULL REFERENCES device(id),
  profile_id TEXT NOT NULL REFERENCES profile(id),
  revealed INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER
);

CREATE TABLE IF NOT EXISTS media (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES library(id),
  title TEXT NOT NULL,
  duration_seconds INTEGER NOT NULL,
  container TEXT NOT NULL,
  codec TEXT NOT NULL
);
