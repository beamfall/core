-- Scraper-precedence locks for identified descriptive metadata (ADR-0009 §8):
-- once identification writes summary/year, file re-imports stop overwriting
-- those fields. Import-derived values keep following the file until then.
ALTER TABLE media ADD COLUMN summary_identified INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media ADD COLUMN year_identified INTEGER NOT NULL DEFAULT 0;
