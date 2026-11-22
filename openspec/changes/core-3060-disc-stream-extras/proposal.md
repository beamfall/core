# Disc stream files beside a titled file are Extras

Ticket: CORE-3060 (`queue:core:main`).

## Why

Disc rips often keep a featurette or trailer as a bare stream file (`00019.m2ts`) in the titled
folder beside the main file. The scan imports it as its own main Media titled `00019`, so it
appears in browse next to the real title. Nothing in the path marks it as an Extra today: no
Extras folder and no `-trailer`-style suffix.

## What Changes

- A file whose name is a bare five-digit disc stream number with an `.m2ts` or `.mts` extension
  is an Extra when its folder sits strictly below the Library root and also holds a titled video
  file (one that is not a disc stream file and not a suffixed Extra). It links extra-of to the
  sole main Media of its folder; with several main Media there it stays an unlinked Extra.
- A folder holding only disc stream files, or a disc stream file directly in the Library root,
  keeps today's behaviour: main Media.
- The rule is a filesystem convention in `internal/scan/scan_extras.go`, used by the scan, Extra
  linking and the first-scan estimate alike. Only a disc stream file reads its folder's listing.
- Already-imported files are corrected by the next scan: every scan sets the stored Extra flag from
  the path rules (CORE-2189), so the existing Media row and file row become an Extra in place,
  keeping their Media ID, watch state and metadata. Nothing is deleted or moved.

## Impact

- Code: `internal/scan/scan_extras.go`; tests in `internal/scan/scan_extras_disc_test.go`.
- No schema, migration, wire or plugin change.
