# Recognised artwork sidecars are never catalogue items

Ticket: CORE-1708 (`queue:core:main`).

## Why

The scan imports every image file as image Media (ADR-0043), so the curated sidecar images that
the local artwork vocabulary (CORE-0359) already recognises, such as `clearlogo.png`, `fanart.jpg`,
`folder.jpg`, `season01-poster.jpg` and `artist.jpg`, also show up as playable items in film, TV,
music and audiobook libraries.

## What Changes

- The walk, the scan's file count and the library estimate skip any image the local artwork
  vocabulary recognises. Folder and container names match by name alone; a `<stem>-<name>` file
  name matches only beside a non-image media file with that stem; any image in an `extrafanart`
  directory matches. The vocabulary table stays the single source of these names.
- `artist` joins the vocabulary as a recognised folder name with no importable slot yet, like
  `logo`, `clearlogo` and `banner`.
- A rescan no longer sees previously catalogued sidecar items, so the existing missing-file path
  marks them missing; nothing is hard-deleted.

## Impact

- `internal/scan` only. No schema, wire or migration change.
- A photo whose name is a vocabulary folder name (for example `cover.jpg`) is no longer an item.
