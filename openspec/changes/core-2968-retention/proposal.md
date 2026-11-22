# Correct Subscription retention (CORE-2968)

## Why

Entries downloaded in one poll share a receipt timestamp, so keep-last-N can discard an entry ahead of older entries in the same feed. A pending imported star or a follower of another Subscription can also be missed before a Media moves to Trash.

## What Changes

- Persist feed position with the download receipt and use it for entries seen in the same poll.
- Decide and move each Media in one catalog write transaction, rechecking every referencing Subscription's policy, pending stars, Favorites and follows; Favorite and Keep writes take the same writer.
- Keep a legacy tied group whole at the keep-last-N cutoff.
- Record one actorless activity entry for each Media moved by retention.
