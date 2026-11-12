## 1. Store

- [x] 1.1 Add the events migration for the watchlist and rating tables
- [x] 1.2 Add the manual-mark playback projection operation and `SetWatched` for Media and Entities
- [x] 1.3 Add watchlist and rating writes, the item-state read and the `watchlist` / `rated` Sources
- [x] 1.4 Cover Profile deletion, per-Media deletion and the orphan prune

## 2. API

- [x] 2.1 Add `setWatched`, `setWatchlisted`, `setRating` and `itemState` to the dispatcher
- [x] 2.2 Emit the `engagement-changed` invalidation and add the Watchlist shelf to the Home layout
- [x] 2.3 Regenerate the wire goldens and add behaviour tests

## 3. Web

- [x] 3.1 Add the item-actions hook and menu
- [x] 3.2 Use them in the player and on the entity page, and refresh shelves on invalidation
