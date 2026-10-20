-- Batch-operation journal for the bulk dry-run / undo envelope (ADR-0009 §6):
-- every bulk mutation (bulk edit, organize, move, merge) executes as ONE batch
-- = ONE activity-log entry = ONE undo unit, with per-item results inside. This
-- journal is the undo unit: a batch row records the op kind and undone state,
-- and each item row carries the affected Media plus a JSON snapshot of the
-- prior state needed to reverse exactly that item (prior field values for an
-- edit, prior file path for an organize/move, the trashed flag for a merge).
--
-- The vocabulary is named by shape, never by domain (invariant #1): kind is one
-- of bulk-edit | organize | move | merge; payload is opaque JSON the store
-- writes and reads, never a queried column. Undo replays the snapshots and sets
-- undone_at_ms so a batch can never be undone twice. Journaled snapshots are
-- catalog state only — destruction still routes through the audited Trash path
-- (invariant #6), so undo of a merge is a Trash restore, never a resurrection
-- of hard-deleted bytes.
CREATE TABLE IF NOT EXISTS op_batch (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  created_at_ms INTEGER NOT NULL,
  undone_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS op_batch_item (
  batch_id TEXT NOT NULL REFERENCES op_batch(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  prior_state TEXT NOT NULL,
  PRIMARY KEY (batch_id, seq)
);

CREATE INDEX IF NOT EXISTS op_batch_item_media_idx ON op_batch_item(media_id);
