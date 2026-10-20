-- PIPE-1 (ADR-0003 §4) — the Unmatched queue. After a scan ingests Media, the
-- identification pipeline scores each item's top candidate against the plugin's
-- per-plugin auto-apply threshold (ADR-0044 §2). At or above it the match is
-- applied autonomously (allowed because Trash is ON, invariant #6); below it the
-- pipeline does NOT guess — it writes a row here, the recoverable path an operator
-- resolves with the "Fix match" mutation.
--
-- The shape is generic (invariant #1): an item keyed by its Media id, the plugin
-- and scraper that searched, and the top candidate's confidence + handle so the
-- Console can present "best guess was X at 0.62, below the 0.75 cut". No domain
-- vocabulary — "movie"/"episode" never appear; the plugin id carries the species.
--
-- One row per Media (the queue is a current-state set, not a log): a rescan that
-- re-scores re-UPSERTs, and a successful auto-apply or a manual Fix-match clears
-- the row. media_id is the primary key so re-queuing an already-queued item
-- replaces its stale score rather than duplicating it.
--
-- Append-only forward migration, row-preserving: a brand-new table, no rebuild of
-- any existing table (catalog 0053 already rebuilt media_file; this touches none).
CREATE TABLE unmatched_queue (
    media_id          TEXT    NOT NULL PRIMARY KEY REFERENCES media(id) ON DELETE CASCADE,
    plugin_id         TEXT    NOT NULL,
    scraper_id        TEXT    NOT NULL,
    -- The top candidate's clamped match confidence in [0,1] and the plugin's
    -- threshold it fell short of, stored so the Console can explain the queue
    -- entry without re-running the scorer.
    confidence        REAL    NOT NULL,
    threshold         REAL    NOT NULL,
    -- The top candidate's scraper handle and display title, so "Fix match" can
    -- pre-fill the picker with the best guess the pipeline already found. Empty
    -- when the search returned no candidates at all (queued for "no match").
    candidate_id      TEXT    NOT NULL DEFAULT '',
    candidate_title   TEXT    NOT NULL DEFAULT '',
    queued_at_ms      INTEGER NOT NULL
);

CREATE INDEX unmatched_queue_queued_idx ON unmatched_queue (queued_at_ms);
