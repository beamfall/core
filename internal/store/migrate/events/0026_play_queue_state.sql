ALTER TABLE play_queue RENAME TO play_queue_legacy;

CREATE TABLE play_queue (
  entry_id TEXT NOT NULL PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
  profile_id TEXT NOT NULL,
  position INTEGER NOT NULL,
  media_id TEXT NOT NULL,
  added_at_ms INTEGER NOT NULL,
  shuffle_position INTEGER
);

INSERT INTO play_queue (entry_id, profile_id, position, media_id, added_at_ms)
SELECT 'legacy-' || lower(hex(randomblob(16))), profile_id, position, media_id, added_at_ms
FROM play_queue_legacy;

DROP TABLE play_queue_legacy;

CREATE INDEX play_queue_profile_position ON play_queue (profile_id, position);
CREATE INDEX play_queue_media ON play_queue (media_id, profile_id);

CREATE TABLE play_queue_state (
  profile_id TEXT NOT NULL PRIMARY KEY,
  current_entry_id TEXT REFERENCES play_queue (entry_id),
  position_ms INTEGER NOT NULL DEFAULT 0 CHECK (position_ms >= 0),
  shuffle INTEGER NOT NULL DEFAULT 0 CHECK (shuffle IN (0, 1)),
  repeat_mode TEXT NOT NULL DEFAULT 'off' CHECK (repeat_mode IN ('off', 'all', 'one')),
  CHECK (current_entry_id IS NOT NULL OR position_ms = 0)
);

-- Queue entries can also be deleted by engagement privacy and retention paths.
CREATE TRIGGER play_queue_clear_current BEFORE DELETE ON play_queue
BEGIN
  UPDATE play_queue_state
  SET current_entry_id = NULL, position_ms = 0
  WHERE profile_id = OLD.profile_id AND current_entry_id = OLD.entry_id;
END;
