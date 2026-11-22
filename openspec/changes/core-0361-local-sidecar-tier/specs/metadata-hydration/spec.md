## ADDED Requirements

### Requirement: Sidecar metadata is written at the locked local tier

The hydration seam SHALL write the local part of a hydration plan at the `local` mutation source,
which ranks below a manual edit and above an operator identification, after validating its fields
and relations against the active bundle. Before writing, the seam SHALL release the Media's
previous local fields to the scan tier. Fields last written at the local tier SHALL read as
locked. A later identification SHALL leave every local field in place.

#### Scenario: An identification keeps the sidecar values

- **WHEN** a Media's title, summary, year and a declared Media field were written from a sidecar
  and an identification later writes different values for them at the operator tier
- **THEN** the Media keeps the sidecar values and reports those fields locked

#### Scenario: A manual edit outranks a sidecar value

- **WHEN** an operator edits a field a sidecar wrote
- **THEN** the manual value lands

### Requirement: A changed sidecar re-hydrates an unchanged file

When the mapper names a sidecar signature field, the scan SHALL re-hydrate an unchanged file
whose current sidecar signature differs from the recorded one, and the seam SHALL record the
current signature after its other writes. A signature that cannot be read SHALL leave the file
as unchanged.

#### Scenario: An edited sidecar refreshes its fields

- **WHEN** a hydrated file's sidecar is edited (its size or modification time changes) and the
  library is scanned again with the media file unchanged
- **THEN** the Media holds the edited sidecar values

### Requirement: A removed sidecar unlocks its fields without clearing them

When a re-hydrated Media's sidecars are gone and a signature was recorded, the seam SHALL release
its local fields to the scan tier and record the empty signature.

#### Scenario: Removing the sidecar unlocks the fields

- **WHEN** the sidecar of a Media with local fields is removed and the library is scanned again
- **THEN** the Media keeps the values, the fields read as unlocked, and a later identification
  replaces them
