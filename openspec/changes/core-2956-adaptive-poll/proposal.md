# Adaptive Subscription poll interval

Ticket CORE-2956 (`queue:core:main`).

## Why

Subscription feeds previously polled on a fixed 1-hour interval regardless of feed update declarations, HTTP cache lifetimes, publication cadence, or long-term dormancy.

## What changes

- Parse channel update hints with explicit precedence: Podcast Index `updateFrequency` (`complete="true"` -> daily, `rrule`, text/ISO-8601 durations), RSS `<ttl>`, and Syndication `updatePeriod` / `updateFrequency`.
- Parse HTTP `Cache-Control: max-age` on both full responses and `304 Not Modified` responses (`no-cache` / `no-store` clears `max-age`).
- Drop dormant feeds (no published entry in 180 days, or `updateFrequency complete="true"`) to daily polling.
- Otherwise use an explicit feed update hint first, then treat `max-age` as a lower bound alongside observed publication cadence (half the median gap across the last 10 unique release dates), clamped to `[15m, 24h]`, falling back to 1 hour.
- Persist adaptive poll hints and hashed entry timestamps per Subscription in `server_config` so `304 Not Modified` responses (even after transient failures) and rolling single-entry or unguided feeds preserve identical cadence and dormancy evaluation across restarts.

## Verification and rollback

Verified via unit tests in `internal/scrape` and `internal/ingest` (precedence, RRULE/ISO durations, `Cache-Control` handling, `304` vs `200` carryover parity, rolling feeds, upgrade compatibility, and a 500-feed load simulation showing >=50% request reduction over fixed hourly polling). Poll state sits in optional `server_config` keys ignored by older builds.
