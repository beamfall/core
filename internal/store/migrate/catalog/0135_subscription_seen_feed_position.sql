-- Earlier positions rank first when several entries share one receipt time.
-- Legacy receipts have no recorded feed position.
ALTER TABLE subscription_seen_entry ADD COLUMN feed_position INTEGER;

-- Record the audit intent with the Trash move so a restart can publish it.
CREATE TABLE subscription_retention_activity (
    operation_id TEXT PRIMARY KEY,
    media_id TEXT NOT NULL,
    library_id TEXT NOT NULL,
    occurred_at_ms INTEGER NOT NULL
) WITHOUT ROWID;
