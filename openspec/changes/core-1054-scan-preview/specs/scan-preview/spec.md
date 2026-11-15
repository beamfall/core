## ADDED Requirements

### Requirement: A rescan preview plans without writing

The server SHALL offer an Admin Session mutation `scanPreview` that walks a Library's roots with
the scan's filters and returns the planned adds, updates, missing and unchanged counts, a bounded
list of changes, a single-use preview token, its expiry and an audit id. The preview SHALL NOT
change catalog state or enqueue a job; its only write SHALL be the activity row whose operation id
is the audit id.

#### Scenario: New file on disk

- **WHEN** a manager previews a Library whose root holds one file the catalog does not have
- **THEN** the preview reports one add, lists that file, returns a token and an audit id that
  names a `scan.preview` activity row, and no scan job is queued

#### Scenario: Long plan

- **WHEN** the planned changes exceed the requested limit
- **THEN** the list is cut at the limit and `truncated` is true while the counts stay complete

### Requirement: A valid preview token commits the scan once

`commitScanPreview` SHALL consume the token and enqueue the previewed scan, writing a
`scan.preview_commit` activity row linked to the preview's audit id. A token SHALL commit at most
once.

#### Scenario: Valid token

- **WHEN** a manager commits a fresh token from their own preview
- **THEN** a scan job is queued for the previewed Library

#### Scenario: Reused token

- **WHEN** the same token is committed a second time
- **THEN** the commit is refused as already used and no job is queued

### Requirement: Stale or expired preview tokens are refused

A commit SHALL be refused when the token has expired or when the Library's roots or catalogued
files changed since the preview, and a refused commit SHALL enqueue nothing.

#### Scenario: Library changed

- **WHEN** a scan or a roots change lands between the preview and the commit
- **THEN** the commit is refused as stale

#### Scenario: Token expired

- **WHEN** the token is committed after its expiry
- **THEN** the commit is refused as expired

### Requirement: The server advertises the preview

The `/health` response SHALL list `scan-preview` in `features` so clients can enable a preview
control only against servers that offer it.

#### Scenario: Health probe

- **WHEN** a client reads `/health`
- **THEN** `features` contains `scan-preview`
