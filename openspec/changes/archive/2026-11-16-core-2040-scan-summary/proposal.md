# Scan jobs report what they added and left unmatched

Ticket: CORE-2040 (`queue:core:main`).

## Why

A finished scan job only says how many files it checked. An owner cannot tell whether a rescan
picked up new files, noticed edits or removals, or left items waiting for a match, so they have
no cue to open the Fix match view.

## What Changes

- The scan job keeps per-file counts of files added, changed and left unmatched, carried in its
  checkpoint so they survive the job yielding. The missing pass records how many files it marked
  missing.
- A completed scan job appends one `scan-summary` job event whose JSON payload holds
  `filesChecked`, `added`, `changed`, `missing` and `unmatched`. It uses the existing free-form
  job event payload read through `jobEvents`, so the published client-core-wire contract does not
  change.
- The Console rescan status reads that event when its scan jobs finish and shows, for example,
  "Checked 1,204 files. 12 added · 3 need a match", with a "Fix unmatched items" link to
  `/manage/unmatched` when anything needs a match. The new strings ship in en, fr, de, es and pt.

## Impact

- Code: `internal/scan`, `internal/web/app` (LibraryRescan, managed-files strings).
- No schema, migration or wire contract change.
