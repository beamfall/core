-- A stream's container title tag (CORE-2523): files often name a subtitle
-- track's audience or region only in its title ("English (SDH)", "Spanish
-- (Latin America)"), and pickers label tracks with it. Empty means untagged;
-- existing rows stay empty until a rescan re-probes the file.
ALTER TABLE media_file_stream ADD COLUMN title TEXT NOT NULL DEFAULT '';
