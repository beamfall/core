# Scan derives the local-metadata class from the file, not a constant

Ticket: CORE-3023 (`queue:core:main`).

## Why

The scanner passed the hardcoded class `image` to local-file extractors and ran them only for still-image
extensions. Camera-roll clips (mov/mp4) therefore never got a capture date or other plugin-extracted
fields, and extractors could not tell a clip from a still.

## What Changes

- Extractors receive class `image` for still-image extensions and `video` for a file whose probe has a
  video stream. Audio-only and unprobed non-image files are not offered to extractors.
- Extracted fields are applied for any file that produced local metadata, not only stills.
- Unchanged files that take the unprobed fast path are not re-extracted until they change.
- A file with extracted local metadata imports standalone (not as a queued part or attached version), so its metadata is applied.
