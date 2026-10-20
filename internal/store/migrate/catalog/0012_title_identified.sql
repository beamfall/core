-- Scraper-precedence lock for the title field (ADR-0009 §8, minimal form):
-- once identification writes a title, file re-imports stop overwriting it.
-- Import-derived titles keep following the file until then.
ALTER TABLE media ADD COLUMN title_identified INTEGER NOT NULL DEFAULT 0;
