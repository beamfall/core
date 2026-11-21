-- Old receipts may contain either a source GUID or an enclosure URL.
ALTER TABLE subscription_seen_entry
  ADD COLUMN guid_source TEXT NOT NULL DEFAULT 'unknown'
    CHECK (guid_source IN ('unknown', 'explicit', 'derived'));
