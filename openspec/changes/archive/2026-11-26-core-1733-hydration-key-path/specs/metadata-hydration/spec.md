## MODIFIED Requirements

### Requirement: Hydration is keyed on the file's content fingerprint

The scan SHALL pass the file's content fingerprint and path to the local metadata hydration hook.
When the hook's mapper declares a fingerprint field, the seam SHALL read the key recorded at the
last hydration and ask the mapper whether to skip, write absent fields only, or replace, recording
the current key for both writes. A mapper with no fingerprint field SHALL keep absent-only writes.

#### Scenario: A re-tagged file shows its new tags

- **WHEN** a hydrated file's tags change (new title and album, new bytes, later mtime) and the
  library is scanned again
- **THEN** the Media's title and album fields hold the new tag values and the recorded key
  carries the new fingerprint

#### Scenario: An unchanged file is not re-hydrated

- **WHEN** a hydrated file is scanned again with the same bytes at the same path, even after its
  mtime changed
- **THEN** the hydration seam writes nothing

#### Scenario: A file hydrated before fingerprints were recorded

- **WHEN** a hydrated file has no recorded key
- **THEN** the hydration seam writes absent fields only and records the current key

## ADDED Requirements

### Requirement: Hydration key includes the mapped plan digest

When the mapper declares a fingerprint field, the hydration seam SHALL build the hydration key
from the content fingerprint and a digest of the plan the mapper produced from the file's tags
and path. An empty content fingerprint SHALL yield an empty key.

#### Scenario: A moved file picks up path-derived facts

- **WHEN** a hydrated file without a disc tag is moved, bytes unchanged, into a disc folder whose
  name the mapper derives a disc number from, and the library is scanned again
- **THEN** the same Media's disc number is the one derived from the new folder

#### Scenario: A move that changes no derived fact is not re-hydrated

- **WHEN** a hydrated file is moved, bytes unchanged, to a path from which the mapper derives the
  same plan
- **THEN** the hydration seam writes nothing
