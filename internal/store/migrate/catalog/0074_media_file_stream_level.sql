-- PLAY-2 compatibility migration: fresh databases receive media_file_stream.level
-- from 0050; databases that already applied the pre-PLAY-2 0050 get the column
-- here. prepareMigrationCompatibility performs the guarded ALTER so this file
-- stays idempotent across both shapes.
SELECT 1;
