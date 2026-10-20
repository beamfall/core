-- PLAY-1 / ADR-0010 §4 compatibility marker.
--
-- Fresh databases receive media_file_stream.hdr10_plus from 0050 because the
-- stream inventory is a first-schema contract. Databases that already applied
-- the legacy pre-PLAY-1 0050 are repaired by the migration runner immediately
-- before this marker is recorded; plain SQL cannot express SQLite's missing
-- "ADD COLUMN IF NOT EXISTS" portably.
SELECT 1;
