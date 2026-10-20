-- Index the duplicate-candidate self-join (ADR-0042 Library Health "duplicate"
-- dimension). dimensionPredicate(HealthDuplicate) and HealthDuplicateGroups both
-- join media_file to itself on oshash to find byte-identical content under two
-- distinct catalog rows; without an oshash index the predicate's inner lookup is
-- a full SCAN of media_file per outer row — O(n^2) in the file count — and it
-- runs for every Library on every scan via RefreshLibraryHealthCache
-- (0043_library_health_cache.sql). With this index that inner lookup becomes a
-- SEARCH on oshash (verified by EXPLAIN QUERY PLAN).
--
-- Partial on missing_at_ms only. The natural instinct is to also exclude the
-- not-yet-fingerprinted ('') rows with `AND oshash <> ''`, but that makes the
-- index UNUSABLE for the self-join: SQLite's partial-index prover cannot derive
-- g.oshash <> '' transitively from f.oshash <> '' across `g.oshash = f.oshash`,
-- so it rejects the index for the g lookup (confirmed via EXPLAIN QUERY PLAN —
-- the join falls back to the full scan). `missing_at_ms IS NULL` is provable (it
-- is an explicit term on g) and excludes missing files, which never participate
-- in duplicate detection. The '' rows are harmless dead weight: every lookup
-- probes a specific non-empty oshash, so the '' cluster is never scanned.
CREATE INDEX IF NOT EXISTS media_file_oshash_idx
ON media_file(oshash)
WHERE missing_at_ms IS NULL;
