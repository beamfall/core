-- CORE-3152: the automated writer that owns an Entity→Media relation row, for
-- example "scraper:<plugin>/<scraper>" for a scraper-declared holder link.
-- NULL means an operator or an unattributed writer. Automated cleanup removes
-- only rows its own writer key owns, so it never deletes a curated link.
ALTER TABLE entity_relation ADD COLUMN writer TEXT;
