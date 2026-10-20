-- ENC-DB-4: initial per-Encrypted-Library metadata DB namespace.
-- Routing/relocation lands later (ENC-DB-3/5); this table anchors the
-- encrypted_library migration set, local schema_migrations, and user_version.
CREATE TABLE encrypted_library_metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
