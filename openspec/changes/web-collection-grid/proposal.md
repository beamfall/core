# Collections as a poster grid in the web viewer

Ticket: CORE-2438 (`queue:core:main`), web first.

## Why

`/collections` lists collections as text rows. Comparable media servers show collections as a
poster grid with an item count, which is how viewers recognise a set at a glance. Collections have
no artwork of their own.

## What Changes

- `/collections` renders each library's collections as a grid of cards: a collage of the first
  four visible members' posters, the collection name and "N items".
- The member posters come from the existing collection feed (`sourceItems` kind `collection`,
  `limit: 4`), batched as aliased fields into one query per 32 collections, so they carry the same
  visibility, Mute and reveal filtering as the collection page itself.
- A collection with no visible member posters shows a placeholder glyph, never a broken image, and
  a failed poster read leaves the card with its placeholder.

## Non-goals

- No schema or wire change. Stored collection artwork (an `artworkSlots` field on
  `CollectionInfo`) is a later server item.
- The library Collections tab is owned by a separate change; it can reuse the card component.

## Impact

`internal/web/app/src/areas/viewer/pages/CollectionListPage.tsx`, `lib/collections.ts`, a new
collection-card stylesheet.
