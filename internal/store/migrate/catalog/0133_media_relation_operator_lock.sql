-- A relation's operator lock is independent of playback certification.
ALTER TABLE entity_relation ADD COLUMN operator_locked_at_ms INTEGER;
ALTER TABLE entity_relation ADD COLUMN operator_lock_activity_id TEXT;
ALTER TABLE entity_relation ADD COLUMN operator_lock_one INTEGER NOT NULL DEFAULT 0;

-- Recovery snapshots share the Media's catalog plane and transaction.
CREATE TABLE media_relation_activity (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  operation_id TEXT NOT NULL UNIQUE,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  relation TEXT NOT NULL,
  before_rows TEXT NOT NULL,
  after_rows TEXT NOT NULL,
  pending_activity TEXT
);
CREATE INDEX media_relation_activity_media_idx ON media_relation_activity(media_id, relation, id);
CREATE INDEX media_relation_activity_pending_idx ON media_relation_activity(id) WHERE pending_activity IS NOT NULL;
