## ADDED Requirements

### Requirement: Sidecar metadata is written at the locked local tier

The hydration seam SHALL write a sidecar's local part at the `local` mutation source, which ranks
below a manual edit and above an operator identification, after validating its fields and
relations against the active bundle. Fields last written at the local tier SHALL read as locked.
A later identification SHALL leave every local field in place.

#### Scenario: An identification keeps the sidecar values

- **WHEN** a Media's title, summary, year and a declared Media field were written from a sidecar
  and an identification later writes different values for them at the operator tier
- **THEN** the Media keeps the sidecar values and reports those fields locked

#### Scenario: A manual edit outranks a sidecar value

- **WHEN** an operator edits a field a sidecar wrote
- **THEN** the manual value lands

### Requirement: A sidecar refresh is one transaction on the Media's catalog

The seam SHALL apply a sidecar's local part in one store transaction on the catalog that holds
the Media, an unsealed encrypted library's included: release the previous local fields to the
scan tier, write the new local values, reconcile the owned relations and record the signature of
the bytes the mapper parsed. A failed apply SHALL leave the previous values, locks and signature.

#### Scenario: A failed refresh keeps the locks

- **WHEN** applying an edited sidecar fails part way
- **THEN** the previous local values stay locked and the old signature stays, so the next scan
  retries

#### Scenario: An encrypted library takes sidecar values

- **WHEN** a Media in an unsealed encrypted library has a sidecar with a title
- **THEN** the title lands at the local tier

#### Scenario: The recorded signature is the parsed sidecar's

- **WHEN** a sidecar is replaced after the mapper read it and before the seam recorded it
- **THEN** the recorded signature is the one the mapper read, and the next scan applies the
  replacement

### Requirement: The recorded sidecar identity names its source

The identity the seam records and compares SHALL be the selected file's path together with the
mapper's signature, so a selection that moves to another file's sidecar refreshes the Media even
when the two sidecars carry the same signature.

#### Scenario: Two versions carry sidecars with the same signature

- **WHEN** a Media's first version's sidecar is removed and its second version has a sidecar of
  the same name, size and modification time but different values
- **THEN** the next scan applies the second sidecar's values

### Requirement: Sidecar relations are owned by the local writer

Relation rows a sidecar wrote SHALL carry the writer `local`. For each relation the sidecar format
owns, the seam SHALL replace the Media's local-writer rows with the sidecar's rows, emptying a
relation the sidecar no longer lists, and SHALL leave every row an operator or another writer
owns. A scraper's upsert SHALL NOT take a local row over. A row a removed sidecar released SHALL
be taken back by the local writer when a sidecar lists it again.

#### Scenario: A manual credit survives a sidecar edit

- **WHEN** a Media has a sidecar credit and an operator-added credit and the sidecar is edited
- **THEN** both credits remain

#### Scenario: Removing the last listed credit removes the local row

- **WHEN** a sidecar that listed one credit is edited to list none
- **THEN** the Media has no local credit

#### Scenario: A restored sidecar reclaims its released credit

- **WHEN** a sidecar's credit is released by removing the sidecar, the sidecar is restored with the
  same credit and then edited to list none
- **THEN** the Media has no local credit

### Requirement: A sidecar with no local values is still applied

A readable sidecar with a signature and no local values SHALL be applied as an empty plan that
owns the format's relations: its previous local fields are released, its owned relations are
emptied and its identity is recorded.

#### Scenario: A populated sidecar is replaced by one with no values

- **WHEN** a hydrated Media's sidecar is replaced by a readable sidecar that names no values
- **THEN** the previous fields read as unlocked, the sidecar's credits are gone and the next scan
  does not apply it again

### Requirement: An unreadable sidecar is not a removed sidecar

When the mapper reports a sidecar that exists but could not be read, or the signature lookup of
any of the Media's files fails for a reason other than a clean not-found, the seam SHALL release
nothing, apply nothing and record no signature, and SHALL NOT move on to another file's sidecar,
so the next scan retries.

#### Scenario: A sidecar unreadable for one scan

- **WHEN** a hydrated Media's edited sidecar cannot be read during a scan and is readable on the
  next one
- **THEN** the old values stay locked after the first scan and the edited values land on the next

#### Scenario: A sidecar lookup fails

- **WHEN** the lookup of a hydrated Media's only sidecar fails because its directory cannot be
  searched
- **THEN** the values stay locked, the relation rows stay owned and the recorded signature stays

### Requirement: A changed sidecar refreshes every file the scan visits

The scan SHALL refresh the sidecars of an unchanged file, and of a file it attaches as a version,
whose sidecar signature differs from the recorded one, and of each file it imports as one of a group of
parts, without needing a media probe. The Media's
sidecar SHALL be read beside the first of its files, in path order, that has one. A signature
check that fails SHALL leave the file as unchanged.

#### Scenario: An edited sidecar refreshes its fields

- **WHEN** a hydrated file's sidecar is edited and the library is scanned again with the media
  file unchanged
- **THEN** the Media holds the edited sidecar values

#### Scenario: A sidecar beside a secondary version

- **WHEN** a Media has two versions and the only sidecar sits beside the second
- **THEN** the Media takes that sidecar's values

#### Scenario: An edited sidecar beside grouped parts

- **WHEN** a Media imported as a group of parts has its sidecar edited and the library is scanned
  again with the parts unchanged
- **THEN** the Media holds the edited sidecar values

### Requirement: A removed sidecar unlocks its values without clearing them

When none of a Media's files has a sidecar and a signature was recorded, the seam SHALL release
its local fields to the scan tier, clearing any identified mark a released field carries, and its
local relation rows to an unowned writer, keeping the values, and record the empty signature, in
one transaction, whether or not a probe succeeds.

#### Scenario: Removing the sidecar unlocks the fields

- **WHEN** the sidecar of a Media with local fields is removed and the library is scanned again
  without a media probe
- **THEN** the Media keeps the values, the fields read as unlocked, and a later identification
  replaces them

#### Scenario: A rescan after removal takes the file's own title

- **WHEN** a sidecar title is released by removing the sidecar and the file is imported again
  with a title from its name
- **THEN** the Media takes the title from the file name
