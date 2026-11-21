# Correct Subscription retention (CORE-2968)

## Why

Entries downloaded in one poll share a receipt timestamp, so keep-last-N can discard an entry ahead of older entries in the same feed. A pending imported star or a follower of another Subscription can also be missed before a Media moves to Trash.

## What Changes

- Persist feed position with the download receipt and use it for entries seen in the same poll.
- Recheck the current policy, pending stars, Favorites and relevant follows before moving Media to Trash.
- Record one actorless activity entry for each Media moved by retention.
