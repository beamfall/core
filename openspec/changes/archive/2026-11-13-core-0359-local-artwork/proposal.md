# Import curated artwork files from disk

Ticket: CORE-0359 (`queue:core:main`, DISC-CORE-LOCAL-ARTWORK-1).

## Why

Operators keep curated artwork next to their media (`poster.jpg`, `fanart.jpg`,
`<stem>-thumb.jpg`, `season01-poster.jpg`). Core ignores all of it: every item gets an embedded
cover or a frame grab, and a scrape can replace whatever is there. Plex, Jellyfin, Emby and Kodi
all treat these files as the highest-precedence artwork source.

## What Changes

- The scan walk discovers sidecar images for each non-image Media using one lookup table, the
  local artwork vocabulary, in jpg, png and webp:
  - file level: `<stem>-poster`, `<stem>-fanart`, `<stem>-thumb`, `<stem>-landscape`, `<stem>-logo`;
  - folder level, only when the folder holds exactly one non-image media file:
    `poster|folder|cover`, `fanart|backdrop`, `thumb|landscape`, `banner`, `logo|clearlogo`, and
    `extrafanart/` as a lower-ranked backdrop source;
  - container level: `season<NN>-poster` and `season-specials-poster`.
  File-level names win over folder-level names for the same slot.
- Matches import into the existing `poster`, `backdrop` and `thumb` slots with source `local`.
  No slot kind is added. `logo`, `clearlogo` and `banner` names are recognised in the table but
  map to no slot yet: the `.jpg` slot files would drop logo transparency, so importing them waits
  for a follow-up even though CORE-0394 added `logo` and `banner` slot names.
- Each locally imported slot carries a file-backed lock marker beside the slot file, recording
  source `local` and the sidecar's path, size and modification time. It works for Media and
  Entity artwork and needs no migration (ADR-0072).
- `ImportArtwork` and `ImportEntityPoster` refuse a locked slot with a typed `SlotLockedError`,
  so a scrape never overwrites local artwork and its candidate is not marked applied. A new
  `ImportManualArtwork` entry point bypasses the lock, because ADR-0072 ranks manual edits above
  locks.
- `GeneratePoster` and the embedded-cover import leave a locked poster alone, so no frame grab
  runs for an item with a local poster.
- Every scan compares each sidecar's size and modification time with the lock marker, so an
  edited sidecar re-imports even when the media file is unchanged. When a sidecar disappears the
  lock is removed and the art stays.
- Sealed and Encrypted Libraries import no local artwork and write no lock marker (ADR-0045).
- Sidecars are read only as regular files (no symlinks), up to a size cap, and only when their
  bytes are a JPEG, PNG or WebP image. Container-level lookups never leave the library root.

## Not in this change

- Container (`season<NN>-poster`) art reaches an Entity only through an optional resolver seam
  that the identifier can implement. The ordered container Entity is created by identification
  hydration, and resolving it from the scan needs an `internal/identify` change. Follow-up.
- Wiring the manual artwork picker (`internal/graph`) to `ImportManualArtwork`. Until then a manual
  pick on a locally locked slot returns `SlotLockedError`. Follow-up.
- Excluding sidecar images from Media admission (they are still also imported as image Media),
  which needs the estimate walk to change with it. Follow-up.
- Multi-part Media are imported outside the completion path and get no local artwork yet.
- `extrafanart/` fills only the single `backdrop` slot; ordered multiple backdrops are
  ART-BACKDROP-MULTI-1.

## Impact

`internal/artwork`, `internal/scan` (sidecar discovery and walk completion). No migration, no
wire-contract change, no new slot kind.
