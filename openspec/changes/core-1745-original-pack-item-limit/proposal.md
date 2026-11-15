# Refuse oversized Original video packs up front

Ticket: CORE-1745 (`queue:core:main`).

## Why

An Original-quality pack seals each item's own bytes, and the BFOP format bounds one item at 4 GiB.
A video source over that bound queued a background build that failed later with a generic
"item exceeds size bound" error, after the client had already been told the pack was building.

## What Changes

- `createOfflinePack` checks the per-item bound for an Original-quality request (explicit
  `original` or no quality) before it queues a job. A video item over 4 GiB is refused with the
  INVALID message "original quality is limited to 4 GiB per item; choose high, medium or low
  quality", and no job is queued.
- The offline pack service exposes the same check as a typed error, so a background build of such
  a request fails with it too.
- Transcoded qualities are unaffected: their bound applies to the transcoded bytes.

## Non-goals

- No change to the BFOP format or its 4 GiB item bound.
- No new wire field: the refusal uses the existing GraphQL error code mechanism.
