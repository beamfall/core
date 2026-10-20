-- Entity arm of the batch-undo journal (FEAT-6 / ADR-0005 §3 entity merge,
-- ADR-0009 §6 undo envelope). The original op_batch_item (0037) is Media-locked
-- (media_id NOT NULL REFERENCES media(id)); an entity merge journals Entities,
-- not Media, so it gets a sibling item table keyed by entity_id. Same shape as
-- op_batch_item: a per-item seq, the affected entity, and an opaque JSON
-- prior_state snapshot the store writes and UndoBatch replays (the re-pointed
-- relations + the trashed loser id, so undo reverses exactly that merge). The
-- op_batch parent row (0037) is shared — one batch row, one undo unit, whether
-- its items live in op_batch_item, op_batch_entity_item, or both.
CREATE TABLE IF NOT EXISTS op_batch_entity_item (
  batch_id TEXT NOT NULL REFERENCES op_batch(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  prior_state TEXT NOT NULL,
  PRIMARY KEY (batch_id, seq)
);

CREATE INDEX IF NOT EXISTS op_batch_entity_item_entity_idx ON op_batch_entity_item(entity_id);
