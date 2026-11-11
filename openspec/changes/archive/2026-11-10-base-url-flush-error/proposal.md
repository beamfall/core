# Report flush failures through the base-URL response writer

Ticket: CORE-0840 (`queue:core:main`).

## Why

When the server runs under a configured base URL, every response goes through a wrapper that
rewrites redirect locations. The wrapper forwards `Flush` to the underlying writer but discards
the result. `http.ResponseController.Flush` stops at the first writer that implements
`http.Flusher`, so a caller flushing through a controller is told the flush succeeded even when the
underlying writer cannot flush or its flush failed. The wrapper already reports `Hijack` and `Push`
failures, so the silent `Flush` is a defect, not an intended limitation.

## What Changes

- The wrapper implements `FlushError() error`, returning the underlying writer's flush result
  through `http.ResponseController`. A controller flushing through the wrapper reports
  `http.ErrNotSupported` or the underlying error.
- The wrapper keeps `Flush()`, so callers that type-assert `http.Flusher` keep working.

## Non-goals

- Routes, response bodies, redirect authority and prefix rewriting are unchanged.
- Other response-writer wrappers.
- Removing the `http.Flusher` method when the underlying writer cannot flush.

## Failure modes

- An underlying writer without flush support: the controller reports `http.ErrNotSupported`.
- An underlying writer whose flush fails: the controller reports that error.
- `Flush()` on the wrapper still ignores the result, as `http.Flusher` has no error return.

## Acceptance evidence

- `internal/server/base_url_response_writer_test.go` flushes through `http.ResponseController` and
  through `http.Flusher` over a supported writer, a writer without flush support and a writer whose
  flush fails, with the wrapper reached through `Server.applyBaseURL`.

## Rollback

Revert the change. No stored state or wire contract depends on it.

## Capabilities

### New Capabilities

- `base-url-response-writer`: how the base-URL response writer forwards optional writer behaviour.

### Modified Capabilities

None.
