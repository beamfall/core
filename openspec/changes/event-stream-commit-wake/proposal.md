# Event streams wake on a commit signal instead of polling

Ticket: CORE-1158 (`queue:core:main`).

## Why

Every open event stream read the outbox four times a second whether or not anything had happened.
A household with a dozen open clients cost the server about fifty idle reads a second, and an event
still waited up to 250 ms for the next poll.

## What Changes

- The store signals in-process readers when an outbox write commits. Each stream waits on that
  signal and reads the outbox only when it fires.
- A stream keeps one slow fallback read, every 30 seconds, for an event written by a process that
  cannot signal (another process on the same data directory).
- The keepalive frame and the idle Scope re-check keep their intervals. They are now timed
  directly instead of being checked on a poll tick, and neither reads the outbox.

## Wire compatibility

None. The frames, their order, the keepalive bound and the stream cap are unchanged; events arrive
sooner.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `event-stream`: streams wake on a commit signal; the cached-scope requirement no longer speaks of
  polls.
