# Item watch actions: mark watched, watchlist and personal rating

Ticket: CORE-1194 (`queue:core:main`)

## Why

A Profile's watched state only changes by playing. A viewer cannot mark something they saw
elsewhere as watched, clear a mark they did not mean, keep a list of things to watch later, or
record what they thought of an item. Other media servers offer all four on every item and on its
containers.

## What Changes

- Three session mutations act on the caller's own Profile: `setWatched`, `setWatchlisted` and
  `setRating`. Each targets one Media or one Entity and returns the item's resulting state.
- `setWatched` on an Entity applies to every visible Media the Entity relates to, including the
  Media of its same-Kind child Entities, so one call covers a whole container at any level.
- A manual watched mark is appended to the engagement event log and replayed by the projection
  rebuild like any other playback event.
- A per-Profile watchlist and a per-Profile rating (1 to 10) are stored in the events database.
- `itemState` reads the watched, watchlisted and rating state of one Media or Entity.
- Two Source kinds, `watchlist` and `rated`, list the Profile's watchlist (newest first) and rated
  items (highest first). The default Home layout gains a Watchlist shelf.
- Each change emits a content-free `client.invalidate` event with reason `engagement-changed` to
  the same Profile's connected sessions. The web viewer refreshes its shelves on that event.
- The web app gains a reusable item-actions hook and menu, used in the player and on the entity
  page.

## Non-goals

- Wiring the actions into the card hover menu (CORE-1192) or the item detail page (CORE-1144).
- Rating as a sort or filter inside the library browse grid (CORE-1190); the `rated` Source is the
  sort and filter this change provides.
- Including the watchlist and ratings in the My Data export document.
- Native client UI.
