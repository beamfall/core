# Per-item metadata editor with field locks that refresh honours

Tickets: CORE-1715 (`queue:core:main`).

## Why

An admin cannot correct one item's details in place. Fix Match (CORE-0707) re-identifies an item
against a different candidate and source precedence (CORE-0194) ranks writers, but there is no
way to type a better title, give an item a sort title, fix its year or overview, choose its
artwork, and keep those choices when the provider is asked again. Field locks exist only for
title, summary and year, and nothing re-runs the provider for an already identified item.

## What Changes

- Catalog migration `0116_item_metadata_editor`: `media_metadata_provenance.candidate_id`
  remembers which candidate identified an item, and a new `media_sort_title` table holds an
  optional sort title. Library browse and its letter rail sort by the sort title when one is set.
- `editMediaMetadata(mediaID, title, sortTitle, summary, year)` writes the given fields at manual
  rank. A manual-rank field is a locked field, so every edited field is locked.
- `refreshMediaMetadata(mediaID)` re-applies the remembered candidate through the identify path at
  operator rank, so locked fields keep their values. An item with no remembered candidate is
  refused with a pointer to Fix Match.
- `mediaMetadata(mediaID)` reads the editable details, whether the item can be refreshed, and its
  locks.
- Lockable fields grow to title, sort title, summary, year and one lock per artwork slot
  (`artworkPoster`, `artworkBackdrop`, `artworkThumb`); a held plugin field such as genres can be
  locked too. Identification and refresh skip a locked artwork slot.
- `setMediaFieldLock` keeps the item's lock list as its result; the web field-locks page now uses it.
- Web: a Manage "Edit item" page (`/manage/items/:mediaID`) edits the details, plugin-declared
  fields (through `bulkEditApply`) and artwork, shows and toggles each lock, and refreshes.
  Collection members link to it.
- Activity kinds `media.metadata_edit` and `media.metadata_refresh`.

## Non-goals

- Matching an unidentified item or choosing another candidate: Fix Match (CORE-0707).
- Changing source precedence ranks (CORE-0194).
- Editing genres stored as related entities; genres are editable where a plugin declares them as a
  Media field.
- The Viewer media detail page's More/Manage link to the editor (follow-up; that page is being
  reworked in another change).
