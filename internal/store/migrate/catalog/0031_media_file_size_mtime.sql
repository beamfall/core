-- Cheap-triple scan fast-path (ADR-0009 content-fingerprint identity, scan
-- re-hash avoidance): a scan that finds an unchanged (path, size, mtime) reuses
-- the stored content fingerprint instead of re-hashing the whole file. These
-- columns hold the file's stat() size and mtime as observed by the scan that
-- last hashed it. 0 means "unknown" (rows that predate this migration, or a
-- file never stat-recorded), which can never match a real stat, so those rows
-- always re-hash on the next scan — the fast-path is purely additive and never
-- changes content identity, only avoids recompute when the cheap triple matches.
ALTER TABLE media_file ADD COLUMN size_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_file ADD COLUMN mtime_ms INTEGER NOT NULL DEFAULT 0;
