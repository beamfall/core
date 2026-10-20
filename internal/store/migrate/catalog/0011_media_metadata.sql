-- Manual identification (ADR-0003 §4): scraper-applied descriptive metadata.
-- Both columns are nullable — unidentified Media simply has neither.
--
-- The media_fts index deliberately still covers title only: extending FTS to
-- summaries is a separate, costed decision (index size + ranking), not a
-- side effect of this migration. Title updates stay searchable through the
-- existing 0006 UPDATE OF title trigger.
ALTER TABLE media ADD COLUMN summary TEXT;
ALTER TABLE media ADD COLUMN year INTEGER;
