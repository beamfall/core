-- Keep the inventory state with the files in this library's private catalog.
ALTER TABLE media_file ADD COLUMN scan_inventory_pending INTEGER NOT NULL DEFAULT 0
    CHECK (scan_inventory_pending IN (0, 1));
