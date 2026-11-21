# Console job text keeps its case

Ticket: CORE-2342 (`queue:core:main`).

## Why

The Console job queue reads a value it has no label for through `humanizeWireValue`, which turns
slug punctuation into spaces and lowercases the rest. That is right for an unknown closed-vocabulary
token (a job kind, status, progress unit or event type) but wrong for free text the server wrote: a
path, a title or a provider name lost its case (2026-11-17 tower QA).

## What Changes

- `humanizeWireValue` humanizes only enum-like wire tokens (lowercase words joined by `.`, `_` or
  `-`). Any other value is shown as sent, trimmed, with its case kept.
- Job progress messages, error text and event reasons stay as sent.

## Impact

- Code: web `src/lib/jobDisplay.ts` and its vitest.
- No server, GraphQL or wire-contract change.
