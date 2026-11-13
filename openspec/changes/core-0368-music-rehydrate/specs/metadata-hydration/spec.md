## ADDED Requirements

### Requirement: Hydration is keyed on the file's content fingerprint

The scan SHALL pass the content fingerprint of the file it hydrates to the local metadata
hydration hook. When the hook's mapper declares a fingerprint field, the hydration seam SHALL read
the fingerprint recorded at the last hydration and ask the mapper how to write: skip, write absent
fields only, or replace. Both write cases SHALL record the current fingerprint. A mapper that
declares no fingerprint field SHALL keep the absent-only write.

#### Scenario: A re-tagged file shows its new tags

- **WHEN** a hydrated file's tags change (new title and album, new bytes, later mtime) and the
  library is scanned again
- **THEN** the Media's title and album fields hold the new tag values and the recorded
  fingerprint is the new one

#### Scenario: An unchanged file is not re-hydrated

- **WHEN** a hydrated file is scanned again with the same bytes, even after its mtime changed
- **THEN** the hydration seam writes nothing

#### Scenario: A file hydrated before fingerprints were recorded

- **WHEN** a hydrated file has no recorded fingerprint
- **THEN** the hydration seam writes absent fields only and records the current fingerprint

### Requirement: Re-hydration never overrides operator or manual values

A replace write SHALL go through the source-ranked field upsert at the scan tier. It SHALL leave
in place any field whose recorded source ranks above the scan tier (an operator identification
or a manual edit) and any field the operator locked (ADR-0072).

#### Scenario: A locked title survives a re-tag

- **WHEN** the operator locked a Media's title and the file is re-tagged with a different title
  and album
- **THEN** the next scan updates the album and keeps the locked title

#### Scenario: A manually edited title survives a re-tag

- **WHEN** a Media's title was last written by a manual edit and the file is re-tagged
- **THEN** the next scan keeps the manually edited title
