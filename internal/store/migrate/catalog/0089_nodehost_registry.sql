CREATE TABLE IF NOT EXISTS nodehost_enrollment_code (
  code TEXT PRIMARY KEY,
  expires_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS nodehost_node (
  id TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  updated_at_ms INTEGER NOT NULL,
  document_json BLOB NOT NULL
);
