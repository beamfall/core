ALTER TABLE activity_entry ADD COLUMN operation_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS activity_entry_operation_id_uq
  ON activity_entry(operation_id)
  WHERE operation_id IS NOT NULL;
