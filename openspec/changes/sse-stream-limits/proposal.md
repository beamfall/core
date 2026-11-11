# Bound the cost of open event streams

Ticket: CORE-0729 (`queue:core:main`).

## Why

Each open `/api/events` (and `/v1/events`) stream polls every 250 ms and re-derives the caller's
full auth scope on every poll: five or more store reads, four times a second, per stream, even when
nothing happens. Nothing limits how many streams one session may hold open, and an idle stream
writes nothing, so a relay or reverse proxy drops it silently and the orphaned handler keeps polling.
Operators cannot see how many streams are open.

## What Changes

- An idle stream writes an SSE comment frame (`: keepalive`) at least every 15 seconds. A failed
  write ends the stream, so a dropped connection releases its handler.
- One session (or API token, or scoped key) may hold at most 8 open streams. Another is refused
  with `429` before any stream headers are written.
- The stream keeps the scope it resolved and re-derives it only when the outbox holds events to
  deliver, or when the cached scope is older than 5 seconds. An idle poll costs one outbox read.
- `/metrics` exposes `beamfall_events_streams_open`, the number of open streams.

## Non-goals

- Pushing outbox changes to streams instead of polling (wake broadcast is a separate item).
- Changing `http.Server` timeouts.
- Making the per-session cap configurable.

## Failure modes

- A revoked, expired or relocked session must never receive an event under its old scope: the scope
  is re-derived before any event batch is filtered, so the existing one-poll revocation bound holds.
- An idle stream whose session was revoked ends at the next periodic re-check (at most 5 seconds),
  without having delivered anything.
- A stream that ends for any reason (client gone, write error, revocation) releases its slot, so the
  cap cannot leak.

## Acceptance evidence

- `internal/server/events_limits_test.go` covers the keepalive, the cap and its release, the store
  and scope reads per idle poll (counting fakes), the periodic re-check and the metrics gauge.
- `TestEventStreamTerminatesOnDeviceRevocation` stays green unchanged.
- `go test -race ./internal/server/ -run 'Event'`.

## Rollback

Revert the change. No stored state or wire contract depends on it; clients already ignore SSE
comment frames.

## Capabilities

### New Capabilities

- `event-stream`: resource limits and liveness of the server-sent event stream.

### Modified Capabilities

None.
