-- Soft-delete for Libraries (master #42): a Library flagged trashed is hidden
-- from all queries and UI surfaces but not hard-deleted, so its media rows,
-- scan schedules, and profile grants remain intact until a future purge job
-- (or an explicit hard-delete) cleans them up. The same trashed_at_ms epoch-ms
-- convention used on Media rows.
ALTER TABLE library ADD COLUMN trashed_at_ms INTEGER;
