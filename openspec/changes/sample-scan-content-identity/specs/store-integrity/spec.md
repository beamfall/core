## MODIFIED Requirements

### Requirement: Managed file identity comes from file content

Managed import and managed convert SHALL store the same `sample1:` identity a scan would compute
from the staged bytes. They SHALL also compute the full SHA-256 digest during staging for legacy
duplicate checks. They SHALL refuse, with a validation error, any admission whose sample or legacy
digest is already cataloged in the destination catalog, including live, missing and trashed rows.
They SHALL check both digests before publishing the staged file and again during catalog admission.
Managed convert SHALL check for duplicates before writing the conversion journal. The existing file
and its Media SHALL remain unchanged.

#### Scenario: Same bytes at two paths

- **WHEN** a file is cataloged by scan or managed admission and the same bytes are imported at a
  second path
- **THEN** the import fails with a validation error and leaves no file at the second path

#### Scenario: Same bytes as a trashed file

- **WHEN** a managed file is deleted to managed trash and identical content is imported
- **THEN** the import fails with a validation error and restoring revives the original Media

#### Scenario: New bytes at a reused path

- **WHEN** a managed file is deleted and a file with a different sample identity is imported at
  the same path
- **THEN** the replacement gets a different Media

#### Scenario: Converted artifact

- **WHEN** managed convert admits a converted artifact
- **THEN** its media file carries the sampled identity of the artifact's bytes

#### Scenario: Managed artifact scanned after admission

- **WHEN** a scan discovers bytes previously admitted by managed import or convert
- **THEN** it uses the same fingerprint and creates no duplicate Media for those bytes

#### Scenario: Existing legacy content

- **WHEN** staged bytes match a cataloged legacy full SHA-256 digest
- **THEN** managed admission refuses the duplicate

#### Scenario: Cataloged conversion target absent on disk

- **WHEN** a live catalog row at the conversion destination matches either staged digest and the
  destination file is absent
- **THEN** conversion refuses the duplicate before writing the journal or publishing the staged
  file, so recovery cannot treat the old destination row as a completed conversion

## ADDED Requirements

### Requirement: Scans bound the reads used for content identity

The `sample1:` identity SHALL append the lowercase hexadecimal SHA-256 digest of an eight-byte
unsigned big-endian file size followed by the sampled bytes in offset order. Files up to
917,504 bytes SHALL contribute all their bytes. Larger files SHALL contribute fourteen 65,536-byte
chunks at offsets floor(i × (size − 65,536) / 13) for i from 0 to 13, inclusive. Large files of
equal size whose samples all match share an identity even when unsampled bytes differ.

#### Scenario: First scan of a large file

- **WHEN** a new large file is scanned
- **THEN** fingerprinting reads 917,504 bytes and duplicate samples resolve to one file identity

#### Scenario: Unchanged legacy file

- **WHEN** the path, recorded size and recorded mtime match a legacy row
- **THEN** the scan reuses the legacy identity without reading bytes for fingerprinting

#### Scenario: Legacy file in an unsealed encrypted Library

- **WHEN** a scan finds the unchanged file after the Library is sealed and unsealed
- **THEN** the scan reuses the fingerprint and Media ID from the Library's own catalog

#### Scenario: Missing legacy file returns to its original path

- **WHEN** the path has only a missing legacy row, even if the returning file has the same stats
- **THEN** the scan samples the file without reading the full legacy digest or reusing the missing
  legacy identity

#### Scenario: Legacy file at the same path with incomplete stats

- **WHEN** an available legacy row has an unrecorded size or mtime and its recorded stats still match
- **THEN** the scan compares the full legacy digest once and keeps the identity and records the
  stats if it matches, or uses the sampled identity if it differs

#### Scenario: Verified Unix-epoch mtime

- **WHEN** the one-time legacy comparison verifies a file whose mtime is the Unix epoch
- **THEN** the catalog distinguishes that timestamp from an unknown mtime after reopening, and
  the next unchanged scan reuses the legacy identity without reading bytes for fingerprinting

#### Scenario: Legacy file moved or changed outside Beamfall

- **WHEN** the scan discovers a legacy file at a new path or with changed recorded stats at the
  same path
- **THEN** the file gets a sampled identity and cannot recover watch state or matches from its
  legacy fingerprint. Existing Part and Version grouping still determines the logical Media.

### Requirement: Sampled Media IDs distinguish the fingerprint scheme

A Media ID derived from a sample SHALL use `media-sample1-` and the first 17 lowercase
hexadecimal digest digits. A legacy Media ID SHALL retain its existing scheme and length.

#### Scenario: First admission of sampled content

- **WHEN** a sample identifies bytes with no existing Media and the catalog derives an ID
  from the fingerprint
- **THEN** the new Media ID has the sampled prefix and the first 17 digits of the sample digest
