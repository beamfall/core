# Extras folder conventions apply only below the Library root

Ticket: CORE-2189 (`queue:core:main`).

## Why

The scan recognises Extras by folder name (`Extras`, `Featurettes`, `Trailers`, `Interviews`,
`Shorts`) and by filename suffix. The folder check walked every ancestor of a file up to the
filesystem root, so a Library stored at `/mnt/Trailers/Films` or `/data/Extras/Movies` marked
every file it holds as an Extra: nothing reached browse, and the first-scan estimate counted zero.

## What Changes

- The folder conventions count only folders strictly below the Library root. The root itself and
  every folder above it are ignored. The filename suffix conventions are unchanged.
- The scan walk, the version grouping pass, the Extra-to-main linking pass and the first-scan
  estimate all apply the same root-bounded rule.

## Impact

- `internal/scan` only. No schema, migration, wire or web change. Libraries already scanned under
  such a folder keep their stored Extra flag until the next scan reimports the files.
