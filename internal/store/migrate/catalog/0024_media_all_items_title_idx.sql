-- Keep the all-items shelf on the catalog index instead of sorting the full
-- visible set on every page.
CREATE INDEX IF NOT EXISTS media_library_title_nocase_idx
ON media(library_id, title COLLATE NOCASE, id)
WHERE trashed_at_ms IS NULL;
