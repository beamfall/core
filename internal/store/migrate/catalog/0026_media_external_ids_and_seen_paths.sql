CREATE TABLE media_external_id (
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  scheme TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (media_id, scheme, value)
);

CREATE INDEX media_external_id_lookup_idx ON media_external_id(scheme, value);

ALTER TABLE subscription_seen_entry
  ADD COLUMN path TEXT NOT NULL DEFAULT '';

ALTER TABLE subscription
  ADD COLUMN retention_kind TEXT NOT NULL DEFAULT 'keep-all'
    CHECK (retention_kind IN ('keep-all','keep-last-n','delete-after-played'));

ALTER TABLE subscription
  ADD COLUMN retention_n INTEGER;

ALTER TABLE subscription
  ADD COLUMN auth_header TEXT NOT NULL DEFAULT '';
