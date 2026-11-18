# Action row on the web Media detail page

Ticket: CORE-2436 (`queue:core:main`).

## Why

The Media detail page offers Play, Start over, Watch together and Next, but a viewer cannot mark the
item watched, add it to the watchlist, rate it or add it to a playlist without going back to a card
menu. Plex and Jellyfin put those actions on the detail page beside Play. The server already has
`itemState`, `setWatched`, `setWatchlisted` and `setRating` for a Media target, and the web already
has the `ItemActions` component that the Entity page uses.

## What changes

- The detail page renders `ItemActions` for the `{mediaID}` target under the play row: Mark watched
  or unwatched, Add to or Remove from watchlist, and a five-star rating. Play stays the one
  accent-filled action.
- A More menu beside the play row offers Add to playlist (the existing manager-gated dialog) and
  Copy link.
- The page's Media read requests the credit `attributes` and `artworkSlots` that `media(id)` has
  carried since CORE-2406, so the cast row can show headshots and characters on this page.
- CORE-2406 follow-up: the cast row names a department even when it is the only one. Audio Media
  shows the cast row too when a person credit carries a headshot or a part (an audio drama's cast),
  and then drops the names under the title so no one is listed twice; audio whose credits are names
  only keeps the linked names under the title.

## Non-goals

- New server reads or mutations; the item-state API is unchanged.
- Native clients.

## Failure modes

- A failed state load or action is reported inline by `ItemActions`, with a retry when nothing has
  loaded. Play is unaffected.
- A device that cannot change playlists sees the dialog's existing explanation.
