-- NGAUDIT-C5-C1-PROBE: persist ffprobe's generic subtitle disposition flags
-- in routed encrypted-library stream inventories. Existing rows default false
-- until a rescan refreshes the byte-keyed inventory.
ALTER TABLE media_file_stream ADD COLUMN is_hearing_impaired INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_file_stream ADD COLUMN is_visual_impaired INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_file_stream ADD COLUMN is_commentary INTEGER NOT NULL DEFAULT 0;
