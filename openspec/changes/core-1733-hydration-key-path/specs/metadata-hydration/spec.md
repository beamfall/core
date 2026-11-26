## MODIFIED Requirements

### Requirement: Hydration is keyed on the file's content fingerprint

The scan SHALL pass the content fingerprint and path of the file it hydrates to the local metadata
hydration hook. When the hook's mapper declares a fingerprint field, the hydration seam SHALL build
a hydration key from the content fingerprint and a digest of the mapper's plan, compare it with the
key recorded at the last hydration and ask the mapper how to write: skip, write absent fields only,
or replace.

#### Scenario: Keys are recorded and absent fingerprints are tolerated

- **WHEN** a mapper writes in either the absent-only or the replace case
- **THEN** the current key is recorded
- **AND** an empty content fingerprint gives an empty key
- **AND** a mapper that declares no fingerprint field keeps the absent-only write

#### Scenario: A re-tagged file shows its new tags

- **WHEN** a hydrated file's tags change (new title and album, new bytes, later mtime) and the
  library is scanned again
- **THEN** the Media's title and album fields hold the new tag values and the recorded key
  carries the new fingerprint

#### Scenario: An unchanged file is not re-hydrated

- **WHEN** a hydrated file is scanned again with the same bytes at the same path, even after its
  mtime changed
- **THEN** the hydration seam writes nothing

#### Scenario: A moved file picks up path-derived facts

- **WHEN** a hydrated file without a disc tag is moved, bytes unchanged, into a disc folder whose
  name the mapper derives a disc number from, and the library is scanned again
- **THEN** the same Media's disc number is the one derived from the new folder

#### Scenario: A move that changes no derived fact is not re-hydrated

- **WHEN** a hydrated file is moved, bytes unchanged, to a path from which the mapper derives the
  same plan
- **THEN** the hydration seam writes nothing

#### Scenario: A file hydrated before fingerprints were recorded

- **WHEN** a hydrated file has no recorded key
- **THEN** the hydration seam writes absent fields only and records the current key
