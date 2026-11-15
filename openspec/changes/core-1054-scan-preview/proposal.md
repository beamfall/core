# Rescan preview with a single-use token

Ticket: CORE-1054 (`queue:core:main`), core part. The Windows Manager part lands in
`beamfall-windows`.

## Why

A manager can start a rescan (`scanLibrary`) but cannot see what it will do first. A rescan can add
many items or mark files missing, and the Windows Manager already ships a disabled Preview rescan
control waiting for the server to offer a preview. The preview must be auditable, and a commit must
not run a plan the manager never saw: an old, reused or out-of-date preview has to be refused.

## What Changes

- New admin mutation `scanPreview(libraryID, root?, limit?)` (Admin Session). It walks the
  Library's roots with the same filters as a scan (excluded folders, supported media, stability
  window, missing-pass guards) and writes no catalog state. It returns counts of planned adds,
  updates, missing files and unchanged files, a bounded list of changes (default 100, at most
  500, `truncated` when cut), the roots whose missing pass would be skipped and why, a
  single-use `previewToken`, its `expiresAt`, and an `auditEventID`.
- The audit id is the operation id of a `scan.preview` activity row written with the preview. If
  that row cannot be written the preview fails.
- New admin mutation `commitScanPreview(previewToken)` (Admin Session). It consumes the token and
  enqueues the scan exactly as `scanLibrary` would, writing a `scan.preview_commit` activity row
  with operation id `<auditEventID>.commit`.
- Tokens are 256-bit random values held in memory as SHA-256 digests, expire after 10 minutes,
  are bound to the Library, root and credential that asked for them, and are spent on first use.
- A commit is refused with a Conflict error when the token was already used, has expired, or is
  stale: the Library's roots or catalogued files changed since the preview (a scan ran, roots
  changed, or the Library was removed or hidden). An unknown token, or one issued to another
  credential, reads as `unknown scan preview token`.
- `/health` gains `features: ["scan-preview"]` so clients can enable a Preview rescan control.
  Protocol features stay out of the capability registry and the compatibility policy.
- Wire contract: two new admin fields and the `ScanPreviewInfo` type. Goldens are regenerated;
  client-core-wire 2.10.0 must be published before merge.

## Non-goals

- Persisting previews across a server restart. A restart drops every token; the manager previews
  again.
- Treating disk-only changes as staleness. The commit runs a real scan, which sees the disk as it
  is then; staleness covers the catalog state the plan was computed against.
- Per-file metadata or artwork planning. The preview reports file-level changes only.
- Plugin directory admission. The scan asks directory-context plugins whether to admit a folder
  and may refuse it or drop catalogued still images there; the preview does not run plugins, so in
  folders a plugin governs it can list adds that the scan refuses. The missing-ratio guard counts
  planned adds, as the scan counts present files after its imports.

## Failure modes

- An unreadable or missing root fails the preview with the scan's root error; no token is issued.
- A refused commit enqueues nothing and the token stays spent.
- A full token table evicts the soonest-expiring preview.

## Acceptance evidence

- `TestScanPreviewPlansWithoutWriting`, `TestCommitScanPreviewRunsOnceForAValidToken`,
  `TestCommitScanPreviewRefusesStaleTokens`, `TestCommitScanPreviewRefusesUnknownToken`,
  `TestScanPreviewRequiresAdminSession` (`internal/graph`).
- `TestPreviewRoots*` and `TestLibraryStateDigestMovesWhenTheLibraryChanges` (`internal/scan`).
- `TestHealthAdvertisesScanPreview` (`internal/server`).

## Rollback

Revert the change. No schema, migration or stored state is added beyond activity rows.
