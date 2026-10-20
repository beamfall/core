CREATE TABLE IF NOT EXISTS pairing_code (
  code TEXT PRIMARY KEY,
  profile_id TEXT NOT NULL REFERENCES profile(id),
  device_role TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  consumed_at INTEGER
);

CREATE INDEX IF NOT EXISTS pairing_code_expires_at_idx ON pairing_code(expires_at);
