## 1. Store

- [ ] 1.1 Add the events migration for the watchlist and rating tables
- [ ] 1.2 Add the manual-mark playback projection operation and `SetWatched` for Media and Entities
- [ ] 1.3 Add watchlist and rating writes, the item-state read and the `watchlist` / `rated` Sources
- [ ] 1.4 Cover Profile deletion, per-Media deletion and the orphan prune

## 2. API

- [ ] 2.1 Add `setWatched`, `setWatchlisted`, `setRating` and `itemState` to the dispatcher
- [ ] 2.2 Emit the `engagement-changed` invalidation and add the Watchlist shelf to the Home layout
- [ ] 2.3 Regenerate the wire goldens and add behaviour tests

## 3. Web

- [ ] 3.1 Add the item-actions hook and menu
- [ ] 3.2 Use them in the player and on the entity page, and refresh shelves on invalidation
