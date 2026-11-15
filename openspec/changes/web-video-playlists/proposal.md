# Video playlists and collection pages in the web viewer

Ticket: CORE-1684 (`queue:core:main`), web and server part.

## Why

An ordered Collection is the playlist (glossary). The server already stores Collections with a
curated order and exposes create, add, remove and reorder mutations, but the web viewer has no page
for them: viewers cannot open a collection, see its entries in order, or play it, and managers have
nowhere to build one.

## What Changes

- `sourceItems` accepts kind `collection` with the Collection id as `value`. It returns the
  collection's members in curated order, paged by `limit` and `offset`, filtered by the same
  visibility (parental, hidden-library) and Mute rules as every other feed. An unknown, trashed or
  invisible collection returns an empty feed. The kind is a free string argument, so the wire
  contract does not change.
- New viewer page `/collections`: every collection the viewer can see, grouped by library, each
  with its entry count. Managers get a form to create a collection in a chosen library.
- New viewer page `/collections/:libraryId/:collectionId`: the entries in curated order and a
  Play all action that opens the first entry with the whole collection as the play queue. Managers
  can switch to editing to move entries up or down, remove entries, and add Media from the same
  library through search.
- Reordering sends the full member order and swaps a visible entry with its visible neighbour, so
  members the viewer cannot see keep their places.
- The account menu links to `/collections`.

## Non-goals

- Native clients (iPhone, tvOS, macOS, Android). Their playlist and collection surfaces are
  separate follow-ups under CORE-1684.
- A plugin-provided box-set source. Collections here are curated by managers.
- Music playlist UI (CORE-1635). The list API stays domain-agnostic.
- Per-Profile private playlists and viewer-role creation: Collections stay library-scoped and
  household-shared, and mutations stay manager-only as today.

## Failure modes

- A save that fails shows an alert and keeps the previous order on screen after a refetch.
- A collection the viewer cannot see, or that was deleted, shows a not-available state.
- An add that fails says so instead of reporting success.

## Acceptance evidence

- Go: `TestCollectionSourceItemsFollowCuratedOrder` and
  `TestCollectionSourceItemsHideWhatTheSessionCannotSee` (`internal/store`).
- vitest: `collections.test.ts` (move rule, paging, ordering), `CollectionListPage.test.tsx`,
  `CollectionPage.test.tsx` (order, Play all queue, reorder keeps hidden members, remove failure,
  add filtered to the library, read-only for viewers, not found).

## Rollback

Revert the change. No schema, migration or stored state is added.
