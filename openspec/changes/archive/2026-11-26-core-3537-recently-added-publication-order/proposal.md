# Order recently-added by publication time for feed Media

Ticket: CORE-3537 (`queue:core:main`).

## Why

`recently-added` lists Media by ingest order (rowid). For feed-catalogued episodes that is the wrong
order: following feeds in a different order, or backfilling an old episode, ranks stale episodes
above fresher ones, and the Podcasts "Latest Episodes" shelf (one tile per show, in item order)
showed the show with the oldest newest-episode first.

## What Changes

- `recently-added` orders Media that have a subscription entry by the entry's effective publish
  time (feed date, else first-seen), newest first. Media without an entry follow, in rowid order.
- A Library set with no subscription entries keeps the existing rowid scan, so the paging fast path
  is unchanged.
- No SDK, wire or client change: the source kind, params and the client's one-tile-per-container
  fold are unchanged.
