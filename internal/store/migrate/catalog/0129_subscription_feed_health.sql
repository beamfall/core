-- Feed health for Library-level Subscriptions (CORE-2955, ADR-0238 §2.2).
--
-- The poller records each feed's health on its own row so the Graph can show
-- why a feed is failing and when it is next tried:
--   last_error_class      the closed class of the most recent failure, '' when healthy
--   failing_since_ms      when the current run of failures began, 0 when healthy
--   consecutive_failures  failures since the last success; drives the backoff
--   next_poll_at_ms       when the feed is next due, 0 means due now
--   retired_at_ms         when the origin answered 410 Gone, 0 when live
--   moved_candidate_url   a pending permanent move awaiting confirmation
--   moved_observations    consecutive polls that reported that same candidate
--   moved_at_ms           when the feed URL was last rewritten by a move
--   refresh_requested_at_ms  the last manual refresh, for its rate limit
-- A failing feed is never disabled: it keeps polling on a backoff capped at
-- one day. Every column defaults to the healthy, due-now state, so existing
-- rows need no backfill.
ALTER TABLE subscription ADD COLUMN last_error_class TEXT NOT NULL DEFAULT '';
ALTER TABLE subscription ADD COLUMN failing_since_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscription ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscription ADD COLUMN next_poll_at_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscription ADD COLUMN retired_at_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscription ADD COLUMN moved_candidate_url TEXT NOT NULL DEFAULT '';
ALTER TABLE subscription ADD COLUMN moved_observations INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscription ADD COLUMN moved_at_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscription ADD COLUMN refresh_requested_at_ms INTEGER NOT NULL DEFAULT 0;
