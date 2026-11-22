# Offline packs carry every Part and its chapter Markers

Ticket: CORE-3016 (`queue:core:main`).

## Why

An audiobook is one Media whose audio files are its Parts (`media_file.part_ordinal`) and whose
chapters are Markers. The offline pack builder refused any Media whose selected Version had more
than one file, and the manifest carried no chapter list, so a multi-file book could not be packed
and a single-file book lost its chapters offline.

## What Changes

- A pack holds one item per Part of each Media's selected Version, in Part order. The Parts of a
  Version must be contiguous from 0, or the build is refused rather than packing half a book.
- `offline_pack_item` gains `part_ordinal` (migration 0129); its key becomes
  (pack_id, media_id, part_ordinal).
- Manifest items gain two optional fields after `sha256`: `part_ordinal` (omitted for 0) and
  `markers` (the Media's chapter Markers, on its Part 0 item only). A single-Part Media without
  chapter Markers produces the same manifest bytes, digest and wrap as before.
- The content route accepts `/api/pack/content/<token>/<media_id>/<part>`; without a part it
  serves Part 0, as before.
- The audiobook plugin exports a pack resolver for kind `audiobook-book` (one book is one Media);
  core registers it through the existing offline pack resolver hook when the plugin is active.
- Offline progress sync reuses the existing event-time guard on `playback_progress`: a replay
  whose event time is older than the stored one does not move the position. A test pins it for
  audio.

## Non-goals

- No client changes: Android (CORE-0221 media class, manifest Parts and Markers, Part fetch) and
  Apple legs are separate tickets.
- No change to BFOP item encryption: an item is bound by its manifest index, not its Media.
- No new progress-merge logic.
