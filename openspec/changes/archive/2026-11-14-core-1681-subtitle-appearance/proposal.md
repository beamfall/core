# Subtitle appearance on the web player, stored per Profile

Ticket: CORE-1681 (`queue:core:main`).

## Why

The web player's subtitle panel only offered text size and a background (none, dim, solid), and
forgot both on reload. Viewers who need a larger, higher-contrast or repositioned caption, for
example to keep it clear of burned-in text, had no way to get one, and a look chosen on the web did
not follow the Profile to its other devices.

## What Changes

- **More controls**: the subtitle panel adds font (sans, serif, monospace, casual), text colour
  (eight caption colours), text opacity, edge style (none, outline, drop shadow, raised, depressed)
  and vertical position (as authored, low, higher, middle, top) beside the existing size and
  background.
- **Both routes**: the look rides `::cue` rules and the cues' own line setting, so it applies to
  direct play and to the hls.js route (hls.js renders through native TextTracks by default). Cues
  that arrive later pick up the position as they become active.
- **Stored per Profile**: every value is a `subtitle.*` key in the Profile's client preferences
  (the `profile_client_prefs` roaming seam, migration 0014), so it is restored on the next play and
  native clients can read the same keys. The keys and values are fixed by the spec delta; a default
  value clears its key. The server stays opaque to them.
- **Reset** clears every appearance key as well as the sync offset.

## Out of scope

- Native clients honouring the keys (their repositories).
- ASS/SSA authored styling (CORE-0781) and the server offset wire (CORE-0782).
