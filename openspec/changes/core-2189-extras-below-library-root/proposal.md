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
- The bound is the configured Library root that holds the scanned folder, not the scan's own
  folder: a scan aimed at a subfolder (for example `<root>/<title>/Extras`) still sees the
  folders between the Library root and it. A scanned folder under no configured root bounds
  itself.
- A scan import now sets the stored Extra flag from the path both ways, so a rescan repairs a file
  that an earlier scan marked as an Extra. Imports that do not classify by path (uploads, managed
  imports and converts) still never clear the flag.

## Impact

- `internal/scan`, plus one import field and the Media upsert in `internal/store`. No schema,
  migration, wire or web change. Libraries already scanned under such a folder show their files
  as main Media after the next scan of the Library.
