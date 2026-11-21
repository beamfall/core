## Why

CORE-2762 and CORE-2786 address duplicate Media created when identical files in one Library
have legacy and sampled fingerprints. The two catalog file rows need distinct identities
so that both paths stay playable.

## What Changes

- After the scan walk, find candidates across the Library by OSHash and size, then verify the
  sampled file against the legacy full-content digest.
- Keep the sampled Media, fold the legacy Media into it as Versions, and retain both file
  identities. Use the existing undoable version merge and combine playback progress.
- Honor undone merges and exclude encrypted Libraries, unavailable files, Extras, related
  Media, and multipart Media. Leave the split in place when verification or merging fails.
