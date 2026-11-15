# Fixes from the owner's first setup test: captions, the job log and the transcoder form

Tickets: CORE-2086 (player), CORE-2084 (job log), CORE-2085 (transcoder form), all on
`queue:core:main`.

## Why

The owner set up a fresh server on 2026-11-15 and hit three things.

- Playback of a file with five muxed text subtitle streams never started. The web player requested
  every caption track at start. The first request for a muxed stream converts it with FFmpeg over
  the whole file, one conversion at a time, and the conversion was bound to its request: when the
  request went away the conversion died and nothing was cached, so each attempt started over.
- A scan's job log was a column of identical `{"type":"progress"}` entries.
- The transcoder temp directory was a free-text field although the server has a folder browse, and
  Save changed nothing on screen.

## What Changes

- The web player gives a caption `<track>` its source only when that track is first shown.
- The server half (a started conversion finishes and is cached when its request is cancelled) is a
  separate change, `core-2086-subtitle-conversion`: `internal/mediahttp` is held by another landing.
- A job's progress history entry stores the update's counters, unit and message, and the Console
  job log shows it as a progress line.
- The Console transcoder form has a Browse button that opens the server folder picker, disables
  Save while the write is in flight and confirms a successful save.

No wire or schema change: the job event payload is already an opaque JSON string.
