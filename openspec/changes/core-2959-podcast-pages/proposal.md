## Why

CORE-2959 (ADR-0238): a followed feed's catalogue page needs per-entry played state, filtering and
sorting, row actions and a control for where new entries go, so a listener can work through a show
without leaving the page.

## What Changes

- Server: `SubscriptionEntryInfo.played` reports whether the caller's Profile played each entry.
- Web: the feed catalogue filters (All, Unplayed, Downloaded) and sorts (Newest, Oldest); each row
  offers Play and Mark played or unplayed.
- Graph: `subscriptionFollow(subscriptionID)` lets any viewer read its own follow state and route.
- Web: a followed feed's page offers a "New entries go to" selector backed by
  `setFollowNewEntryRoute` (CORE-2958).

## Out of scope

- Seasons, serial ordering, funding and description (need the feed parser, CORE-1150 / CORE-2965).
- Per-entry download, bulk select and entry duration.
