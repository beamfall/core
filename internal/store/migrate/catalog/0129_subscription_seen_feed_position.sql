-- Earlier positions rank first when several entries share one receipt time.
-- Legacy receipts have no recorded feed position.
ALTER TABLE subscription_seen_entry ADD COLUMN feed_position INTEGER;
