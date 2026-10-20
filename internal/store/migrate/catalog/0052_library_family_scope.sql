-- Per-Library family scope (ADR-0028 §1): a Library bound to a Domain Plugin
-- may carry a plugin-declared family scope chosen at creation — the Library
-- "type" (the Film & TV plugin offers films / tvshows / mixed). The scope is
-- opaque plugin data: core stores it verbatim and never interprets the value;
-- identification reads it to fix media classification deterministically for
-- the Library instead of guessing per file. Empty string is the unset scope
-- (no creation-time choice — preserves today's per-file behavior), so the
-- column is NOT NULL DEFAULT '' rather than a nullable column.
ALTER TABLE library ADD COLUMN family_scope TEXT NOT NULL DEFAULT '';
