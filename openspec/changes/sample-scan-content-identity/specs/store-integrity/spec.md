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

- **WHEN** the path, recorded size and non-zero recorded mtime match a legacy row
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

#### Scenario: Legacy file moved or changed outside Beamfall

- **WHEN** the scan discovers a legacy file at a new path or with changed recorded stats at the
  same path
- **THEN** the file gets a sampled identity and cannot recover watch state or matches from its
  legacy fingerprint. Existing Part and Version grouping still determines the logical Media.

### Requirement: Proxy promotion recognizes both content schemes

Proxy promotion SHALL refuse a cataloged identity matching the completed archive's sampled or
legacy digest before replacing the archive path. It SHALL check both digests again in the
generation update and store the sampled digest on successful promotion. The existing GUID-based
Media ID SHALL survive promotion. The duplicate check covers retained rows in the public catalog.

#### Scenario: Archive promotion encounters cataloged content

- **WHEN** the completed archive matches a sampled or legacy file already in the catalog
- **THEN** promotion is refused and the existing file retains its path

### Requirement: Download ingest recognizes both content schemes

Download ingest SHALL skip files whose sampled or legacy digest matches a live row in the public
catalog and leave the existing catalog path and the source file in place.

#### Scenario: Download ingest encounters sampled content

- **WHEN** the incoming bytes match a sampled file already in the catalog
- **THEN** relocation is skipped and the existing file retains its path

### Requirement: Sampled Media IDs distinguish the fingerprint scheme

A Media ID derived from a sample SHALL use `media-sample1-` and the first 17 digest digits.
A readable web address for that ID SHALL tag the digest with `sample1.`. At the route root, a
bare `media-sample1-` address SHALL resolve to the SHA-256 ID with the same digest digits when
the sampled ID is absent.

#### Scenario: Identical digest prefix across schemes

- **WHEN** a sampled Media ID and a legacy Media ID have the same 17 digest digits
- **THEN** the viewer resolves their tagged and untagged addresses to their respective IDs

#### Scenario: Generating addresses for a legacy title with the sampled ID slug

- **WHEN** a legacy title has the slug `media-sample1`
- **THEN** the viewer qualifies its readable SHA-256 address with the Library slug, or generates
  the bare SHA-256 ID when the Library slug is unavailable

#### Scenario: Previously issued unqualified legacy bookmark

- **WHEN** a viewer opens `/watch/media-sample1-a371bff46385f7ded` and the sampled ID is absent
- **THEN** the viewer resolves the bookmark to `media-sha256-a371bff46385f7ded`

#### Scenario: Temporary lookup failure

- **WHEN** the sampled lookup for an ambiguous bookmark fails temporarily
- **THEN** the viewer retries while the address and viewer identity remain current and selects
  the legacy ID only after a confirmed sampled miss

#### Scenario: Expand the docked player

- **WHEN** the listener expands the docked player for a sampled ID
- **THEN** the active playback session survives address resolution and attaches to the full player
