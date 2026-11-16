# Web listening browse: track lists, person pages, playlists, favourites and history

Ticket: CORE-1635 (`queue:core:main`).

## Why

The web viewer shows every Entity's Media as a poster grid. For audio Media that hides what a
listener needs: the order of a container's Media, their runtimes and credits, a way to start the
player from any of them with the rest queued, the containers a person appears on, and the
listener's own playlists, favourites and history. Core stays domain-agnostic, so the layout must
come from generic signals the server already owns, not from plugin Kind names.

## What Changes

- GraphQL (read-only, additive): `EntityInfo.archetype`, `RelatedEntityGroupInfo.archetype`,
  `MediaInfo.durationSeconds` and `MediaInfo.mediaClass` expose existing data, and `sourceItems`
  gains the `favorites` kind (the calling Profile's favourited Media and Entities, newest first).
  The client-core-wire contract needs a new minor version.
- Layout rule (documented in `internal/web/app/src/areas/viewer/lib/trackList.ts`): an Entity
  whose archetype is `container` and whose listed Media are all of class `audio` lists them as
  numbered rows in server order, with runtime and the person credits that differ from the
  container's own people. An `audio` Entity of archetype `person` lists top tracks (the Profile's
  `top-tracks` ranking narrowed to its Media, then server order) and, separately, the containers
  credited on its Media that it is not itself related to ("appears on"). Every other Entity keeps
  the poster grid. Play from a row starts the player with the listed rows as the queue.
- Playlists are Collections: the viewer lists, creates, renames, reorders, removes from and
  deletes (to trash) them, and every Media card menu offers "Add to playlist". Mutations need a
  manager device, as the Collection mutations already require.
- Favourites (`/favorites`) and history (`/history`: recently and most played) pages, linked from
  the account menu.
- Card menus use listening wording ("Mark played") for Media of class `audio`.

## Gaps (not in this change)

- Disc grouping: delivered by `core-1635-web-music-browse` from core's stored `sequence` and
  `index` Media fields, with no schema change.
- A per-person server ranking of top tracks; the Profile's own `top-tracks` ranking is used.
- Playlist membership across Libraries: the server rejects it and the viewer reports the failure.
