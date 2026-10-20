-- NGAUDIT-C5-C1-PROBE compatibility marker.
--
-- Fresh databases receive the disposition columns from 0050 because stream
-- inventory is a first-schema contract. Databases that already applied the
-- previous 0050 shape are repaired by the migration runner immediately before
-- this marker is recorded; SQLite has no portable ADD COLUMN IF NOT EXISTS.
SELECT 1;
