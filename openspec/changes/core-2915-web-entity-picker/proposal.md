# Search and pick Media and containers instead of typing IDs

Ticket: CORE-2915 (`queue:core:main`). Splits the media-ID bullet out of CORE-2450.

## Why

Manage and Console fields ask a manager to type a raw Media, Entity or Collection-member id. A
manager rarely knows an id; they know a title. The owner asked (2026-11-21) to search and pick the
item instead, and to edit it from there.

## What Changes

- Add one web control, `EntityPicker` (single) and `EntityMultiPicker` (several), in
  `internal/web/app/src/components/beamfall/`. It is an ARIA combobox built on the Astryx
  `Combobox` facade that searches by title through the existing `searchResult` GraphQL field and
  shows each row's poster, kind and Library. It returns the picked id.
- A pasted id or Media address still works: the list offers "Use this ID" for typed text that has
  no spaces.
- A picked item shows its title, kind and Library with an Open link (viewer detail) and, for
  Media, an Edit link to Manage item details.
- Replace the raw id inputs: Manage Entities, Item details, Artwork, Field locks, Markers, Bulk
  edit (several), Collection members (several), Tags assign target and bulk targets, Curation
  subject, relation target and extra; Console Playback cache precompute.

## Non-goals

- No server change: `searchResult` already returns id, kind, artwork, Library and entity kind.
- Redesigning the Tags, Curation or Unmatched pages (other tickets own them).
- Resolving titles for ids that were pasted rather than picked.
