-- Per-item metadata editor (CORE-1715). candidate_id keeps the provider
-- candidate the last identification applied, so a per-item refresh can re-run
-- that same provider lookup without a new search. Rows written before this
-- column existed read as '' and refresh asks for a Fix Match instead.
ALTER TABLE media_metadata_provenance ADD COLUMN candidate_id TEXT NOT NULL DEFAULT '';

-- An admin-set sort title, one row per Media item that has one. Browse sorts
-- and letter-indexes by it in place of the title; no row means sort by title.
CREATE TABLE IF NOT EXISTS media_sort_title (
  media_id TEXT PRIMARY KEY REFERENCES media(id) ON DELETE CASCADE,
  sort_title TEXT NOT NULL
);
