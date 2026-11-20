-- File stat verification and embedded inventory have separate lifetimes.
ALTER TABLE media_file ADD COLUMN scan_inventory_pending INTEGER NOT NULL DEFAULT 0
    CHECK (scan_inventory_pending IN (0, 1));
