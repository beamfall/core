# CORE-3004: retry MusicBrainz busy 503s in place

## Why

MusicBrainz answers about 60% of requests with HTTP 503 and `Retry-After: 1` while its global rate
limiter is busy. The scrape engine retried only 429 in place, so each 503 ended the entity
enrichment pass. On Tower the album-genre pass matched one album and then paused every two minutes.

## What Changes

- The scrape engine retries a 503 that carries `Retry-After` in place, under the same three-retry
  cap as 429. A 503 without `Retry-After` is surfaced at once.
- An honoured `Retry-After` is capped at 10 seconds; a longer wait surfaces the error so a walking
  caller ends its pass and requeues.
- The enrichment pause log names the provider error.

## Impact

- `internal/scrape/scrape.go`, `internal/scrape/fetch.go`, `internal/identify/entity_enrichment.go`.
- Ticket: CORE-3004.
