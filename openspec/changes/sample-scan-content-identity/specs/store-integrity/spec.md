## MODIFIED Requirements

### Requirement: Managed file identity comes from file content

Managed import and convert SHALL store the scan's `sample1:` identity for new rows and compute the
full SHA-256 during staging. At catalog admission, either digest matching a row in another
library or at another path, including missing and trashed rows, SHALL cause a validation error.
The existing file and Media SHALL remain unchanged.

#### Scenario: Same bytes at two paths

- **WHEN** bytes cataloged with a sampled or legacy fingerprint are imported at a second path
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
- **AND** its first scan records file stats and OSHash and attempts to collect embedded stream
  and attachment inventory. An unavailable or failing probe does not prevent storing OSHash.

#### Scenario: Managed reimport at an existing legacy path

- **WHEN** managed admission replaces a file removed outside Beamfall and the staged full digest
  matches a legacy row at the same path in the same library, including a missing file or a
  soft-trashed Media
- **THEN** the admission preserves the legacy fingerprint, Media ID, watch state, and match pins
- **AND** matching stats stay recorded for a zero-read scan, while stale stats are cleared so the
  next scan verifies the legacy digest once and records the replacement inode's stats
- **AND** a soft-trashed Media retains its original trash timestamp and remains restorable

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

Mtime comparisons SHALL round Unix nanoseconds toward zero to catalog milliseconds, including
timestamps before the Unix epoch.

#### Scenario: Legacy file in an unsealed encrypted Library

- **WHEN** a scan finds the unchanged file after the Library is sealed and unsealed
- **THEN** the scan reuses the fingerprint and Media ID from the Library's own catalog

#### Scenario: Legacy file at the same path with incomplete stats

- **WHEN** an available legacy row has an unrecorded size or mtime and its recorded stats still match
- **THEN** the scan compares the full legacy digest once at that path and keeps the identity and
  records the stats if it matches, or uses the sampled identity if it differs

Scan deduplication and move detection SHALL compare exact fingerprint strings. The one-time
comparison of an available legacy row with incomplete stats is the sole full-digest scan path.

#### Scenario: Verified Unix-epoch mtime

- **WHEN** the one-time legacy comparison verifies a file whose mtime rounds to the Unix epoch
- **THEN** the catalog distinguishes that timestamp from an unknown mtime after reopening, and
  the next unchanged scan reuses the legacy identity without reading bytes for fingerprinting

#### Scenario: Legacy file moved, changed, or returns after a missing mark

- **WHEN** a scan finds a legacy file moved, with changed recorded stats, or already marked missing
- **THEN** the file gets a sampled identity even if unchanged since it went missing, or if it fits
  entirely in the sample. It cannot recover watch state or matches from its
  legacy fingerprint. Existing Part and Version grouping still determines the logical Media.

### Requirement: Sampled Media IDs distinguish the fingerprint scheme

A Media ID derived from a sample SHALL use `media-sample1-` and the first 17 lowercase
hexadecimal digest digits. A legacy Media ID SHALL retain its existing scheme and length.

#### Scenario: First admission of sampled content

- **WHEN** a sample identifies bytes with no existing Media and the catalog derives an ID
  from the fingerprint
- **THEN** the new Media ID has the sampled prefix and the first 17 digits of the sample digest

### Requirement: Duplicate checks recognize both fingerprint schemes

Before publication, proxy promotion and qBittorrent ingest SHALL treat a match on either fingerprint in the live public catalog as a duplicate.
Missing files and trashed Media and Libraries SHALL be excluded. Proxy promotion SHALL repeat the lookup in the publication transaction and keep a duplicate archive only when a bounded query verifies ownership.
The open tee SHALL match the completed tee's inode, size and mtime before and after fingerprinting.

#### Scenario: Duplicate in either scheme

- **WHEN** a live row matches either digest, including a sampled match with different unsampled bytes
- **THEN** the existing Media and file remain intact and no second archive or incoming file is published

#### Scenario: Download path changes during fingerprinting

- **WHEN** qBittorrent ingest computes both fingerprints from one validated open download file
  and the download path no longer names that file after hashing
- **THEN** ingest reports a fingerprint failure before the duplicate lookup or relocation

#### Scenario: Concurrent sampled admission during promotion

- **WHEN** a matching sample is cataloged before the promotion transaction starts
- **THEN** promotion skips the duplicate and keeps the existing cataloged file at its path,
  including when that path is the archive destination

#### Scenario: A scan discovers a copy of a promoted archive

- **WHEN** proxy promotion publishes an archive and a scan later discovers identical bytes
- **THEN** the scan resolves the bytes to the promoted Media through its `sample1:` fingerprint

### Requirement: Export manifests verify complete originals

The photo export manifest SHALL record `sha256:` followed by the SHA-256 digest of the full
plaintext written for each original.

#### Scenario: Export of sampled content

- **WHEN** a photo export writes an original from a row with a sampled identity
- **THEN** the manifest digest verifies the complete exported bytes
