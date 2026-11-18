# Media versions: unlabelled copies of one title join as Versions

Ticket: CORE-2473 (`queue:core:main`).

## Why

Tower QA on 2026-11-18 (core 3efdff24) found two copies of one film in its folder,
`Disclosure.Day.2026.REPACK.1080p.Blu-ray.Remux...mkv` and `Disclosure.Day.2026.2160p.WEB-DL...mkv`,
imported as two Media. The film and TV plugin parses both to the same title and year and reports no
Version label for release-style names, so the second file asked for the default Version slot, which
the first file already held. The store refused the attach (`version slot "" is taken`) and the scan
imported the file as its own Media. Every later rescan then refuses the attach because the bytes
belong to another Media, so the split never heals. Search and the library list the film twice, and
the Version picker never appears.

## What Changes

- **Scan grouping**: when neither the joining file nor the file it groups with carries a parsed
  label, the scan asks the store to label both from facts core already holds, instead of asking for
  the default slot. The store tries, in order: the probed resolution class (`2160p`, `1080p`, ...,
  judged by width as well as height so a letterboxed 1920x800 frame is `1080p`), the container
  (`MKV`, `MP4`). It takes the first kind that tells the two files apart and whose slots are free,
  moves the file in the default slot to its label and stores the new file under its own. A file
  joining a Media whose files are already labelled takes the first of its own labels that is free. When nothing tells them apart, the file imports as its
  own Media, as before. File names are not a fallback: two same-titled files that differ only by
  name may be different items a plugin could not tell apart, and they stay apart.
- Labels are stored once. A rescan re-imports the same bytes into the slot they hold, so labels
  stay put however the files' probes later change.
- The labels are generic file facts (frame size, container), so the rule needs no domain knowledge; plugins still decide which files are one title.

## Not in this change

- Merging a split that earlier scans already made (two Media for one title). Doing that safely
  means moving every per-Media record (playback progress and history, ratings, favourites,
  Collection membership, play queues, offline packs) and making the merge undoable; it is planned
  as a follow-up ticket. Until then a library that already holds the split keeps it.

## Impact

- `internal/store/media_versions.go` (no migration), `internal/scan/scan_walk_versions.go`.
- No wire change: Version labels are already free text on `playbackInfo.versions`.

## Rollback

Revert the change. Labels already stored stay valid Version labels.
