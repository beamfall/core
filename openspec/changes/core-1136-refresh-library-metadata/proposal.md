# Refresh all metadata for one Library

Ticket: CORE-1136 (`queue:core:main`), the "Refresh all metadata" half.

## Why

`refreshMediaMetadata` (CORE-1715) re-runs the provider for one item at a time. After a provider
fixes its data, or after a plugin update, an operator has to open every item in a Library to pick
the change up. There is no Library-wide refresh and no way to watch one run in Operations.

## What Changes

- A durable background job, `library-metadata-refresh`, walks every live (not trashed) Media in
  one Library and re-applies each item's remembered candidate through the same identify path
  that `refreshMediaMetadata` uses. Locked fields, locked artwork slots and pinned matches keep
  their values exactly as they do for a single-item refresh.
- An item with no remembered candidate, or whose plugin or scraper is no longer available, is
  skipped and counted. Any other per-item failure is counted and the walk goes on.
- The job paces provider calls (a fixed delay between items) and checkpoints by Media id after
  each small batch, yielding between batches through the existing job scheduler. No new queue or
  rate limiter is added; the scraper's existing HTTP 429 back-off still applies to each call.
- The job dedupes on the Library id: a second request while one is queued, running or yielded
  returns the active job, as a scan does.
- Progress (`current`/`total` items, message "Refreshing metadata") is reported through the
  existing jobs model, so the job shows in Operations with its Library as its target and can be
  canceled there.
- GraphQL mutation `refreshLibraryMetadata(libraryID)` returns the job. It needs an Admin Session
  or an agent, cli or diagnostics API token, the same authority as `scanLibrary`.
- Web Console: each Library card offers "Refresh all metadata" behind a confirmation that says it
  may take a while. Strings ship in every Console locale, and the Operations queue names and
  describes the new job kind.

## Non-goals

- "Analyze media" (the ticket's other half): media analysis per Library is a separate change.
- Identifying unmatched items or choosing new candidates: Fix match and auto-identify own that.
- A general provider rate limiter or a new job queue.
- The web Library browse page and its display options (another change owns them).

## Failure modes

- Provider throttling: the per-item delay and the 429 back-off bound request rate; an item that
  still fails is counted as failed, not retried in the same run.
- Cancel: canceling the job loses its lease; the handler stops before the next item and records
  nothing further.
- Restart or preemption: the job resumes from its last checkpointed Media id.
- A sealed encrypted Library has no readable catalog plane; the job fails with that store error.

## Acceptance evidence

Go tests in `internal/identify` and `internal/graph` cover job creation, dedupe, cancel, locked
fields kept, skipped items and progress; a vitest spec covers the Console action;
`internal/conformance` stays green.

## Rollback

Revert the change. The job kind has no schema migration; any queued job of the kind would fail
with "no handler" after a rollback and can be canceled from Operations.

## Capabilities

### New Capabilities

- `library-metadata-refresh`: Library-wide metadata refresh job, its mutation and Console action.

### Modified Capabilities

None.
