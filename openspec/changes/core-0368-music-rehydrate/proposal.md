# Re-hydrate a re-tagged file's local metadata, keyed on its content fingerprint

Ticket: CORE-0368 (`queue:core:main`, DISC-CORE-MUSIC-REHYDRATE-WIRE-1). ADRs 0029 and 0072.

## Why

Local metadata hydration writes only fields that are absent. When a tag editor rewrites a file's
tags, the scan re-probes the changed file and calls the hydration hook again, but every field the
first hydration wrote is already present, so the new title and album never land. The plugin side
already decides how a rescan should write (`Fresh`, `Skip` or `Replace`) from the content
fingerprint recorded at the last hydration and the one the scan computed. Core never passes the
fingerprint to the hook and has no write path that replaces values, so that decision cannot run.

## What Changes

- `scan.MetadataHydrationInput` carries the file's content fingerprint. The scan passes the hash
  it computed for a changed file and the stored fingerprint on the backfill path.
- A hydration mapper can opt in to fingerprint-keyed hydration by implementing an optional
  interface that names a declared text field for the recorded fingerprint and decides the write.
  Mappers that do not opt in keep today's absent-only behaviour.
- For an opted-in mapper, the hydration seam reads the recorded fingerprint and acts on the
  mapper's decision:
  - `Skip`: no write.
  - `Fresh`: the existing absent-only write.
  - `Replace`: a source-ranked upsert at the scan tier, the tier the absent-only path and the
    import record. Tag values replace earlier tag or filename values. Values from an operator
    or a manual edit win (ADR-0072), and so does a field the operator locked.
  - Both write cases record the current fingerprint.
- Not in this change: the music plugin composition in `cmd/beamfall` still has to implement the
  optional interface by delegating to `music.HydratedFingerprintField` and
  `music.DecideHydrationWrite`. That file is outside this attempt's declared scope.
