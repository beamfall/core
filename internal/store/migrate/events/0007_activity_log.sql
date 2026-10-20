CREATE TABLE IF NOT EXISTS activity_entry (
  id INTEGER PRIMARY KEY,
  occurred_at_ms INTEGER NOT NULL,
  action TEXT NOT NULL,
  session_id TEXT NOT NULL DEFAULT '',
  device_id TEXT NOT NULL DEFAULT '',
  profile_id TEXT NOT NULL DEFAULT '',
  target_type TEXT NOT NULL DEFAULT '',
  target_id TEXT NOT NULL DEFAULT '',
  library_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS activity_entry_profile_idx
  ON activity_entry(profile_id, id);

-- ADR-0012 §11: the activity log is append-only. The write layer exposes no
-- update/delete path; these triggers make the contract hold even against a
-- future code path that forgets.
CREATE TRIGGER IF NOT EXISTS activity_entry_append_only_update
BEFORE UPDATE ON activity_entry
BEGIN
  SELECT RAISE(ABORT, 'activity log is append-only');
END;

CREATE TRIGGER IF NOT EXISTS activity_entry_append_only_delete
BEFORE DELETE ON activity_entry
BEGIN
  SELECT RAISE(ABORT, 'activity log is append-only');
END;
