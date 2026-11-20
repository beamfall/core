## MODIFIED Requirements

### Requirement: Managed file identity comes from file content

Managed import and convert SHALL store `sample1:` for new rows and compute the full SHA-256 during
staging. Either digest matching a row at another path or in another Library SHALL cause a validation
error, including for missing or trashed rows, and preserve the existing file and Media.

#### Scenario: Same bytes at two paths

- **WHEN** bytes cataloged with a sampled or legacy fingerprint are imported at a second path
- **THEN** the import fails with a validation error and leaves no file at the second path

#### Scenario: Same bytes as a trashed file

- **WHEN** a managed file is deleted to managed trash and identical content is imported
- **THEN** the import fails with a validation error and restoring revives the original Media

#### Scenario: New bytes at a reused path

- **WHEN** a file with a different sampled identity is imported at a deleted file's path
- **THEN** the replacement gets a different Media

#### Scenario: Converted artifact

- **WHEN** managed convert admits a converted artifact
- **THEN** its media file carries the sampled identity of the artifact's bytes

#### Scenario: Managed artifact scanned after admission

- **WHEN** a scan discovers bytes previously admitted by managed import or convert
- **THEN** it finds the existing identity, records stats and OSHash, and attempts embedded stream
  and attachment inventory. An unavailable or failing probe does not prevent storing OSHash.

#### Scenario: Managed reimport at an existing legacy path

- **WHEN** the staged full digest matches a legacy row at the same path and Library after external removal
- **THEN** admission preserves the legacy fingerprint, Media ID, watch state, and match pins, including
  for missing files and soft-trashed Media, which retain their trash timestamp and remain restorable
- **AND** it keeps matching stats and clears stale stats for the next scan to verify and record once

## ADDED Requirements

### Requirement: Scans bound the reads used for content identity

`sample1:` SHALL precede the lowercase hex SHA-256 of the eight-byte unsigned big-endian file size
and the sampled bytes in offset order. Files up to 917,504 bytes SHALL contribute all bytes. Larger
files SHALL contribute fourteen 65,536-byte chunks at floor(i × (size − 65,536) / 13) for i = 0…13.
Equal sizes and samples share an identity even when unsampled bytes differ.

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
- **THEN** reopening preserves the timestamp and the next unchanged scan reuses the legacy identity without reads

#### Scenario: Legacy file moved, changed, or returns after a missing mark

- **WHEN** a legacy file moves, changes recorded stats, or returns after a missing mark
- **THEN** it gets a sampled identity even if unchanged or small enough to sample whole. It cannot
  recover legacy watch state or matches by fingerprint. Part and Version grouping still applies.

### Requirement: Sampled Media IDs distinguish the fingerprint scheme

A Media ID derived from a sample SHALL use `media-sample1-` and the first 17 lowercase hex digest
digits. Legacy Media IDs SHALL retain their existing scheme and length.

#### Scenario: First admission of sampled content

- **WHEN** the catalog derives a new Media ID from a sample
- **THEN** the ID has the sampled prefix and the first 17 digest digits

### Requirement: Duplicate checks recognize both fingerprint schemes

Before publication, proxy promotion and qBittorrent ingest SHALL treat a match on either fingerprint in the live public catalog as a duplicate.
Missing files and trashed Media and Libraries SHALL be excluded. Proxy promotion SHALL repeat the lookup in the publication transaction and keep a duplicate archive only when a bounded query verifies ownership.
The open tee SHALL match the completed tee's inode, size and mtime before and after fingerprinting.

#### Scenario: Duplicate in either scheme

- **WHEN** either digest matches a live row, including a sample with different unsampled bytes
- **THEN** the existing Media and file remain intact, without publishing another file

#### Scenario: Download path changes during fingerprinting

- **WHEN** the download path no longer names the validated descriptor used to compute both digests
- **THEN** ingest reports a fingerprint failure before the duplicate lookup or relocation

#### Scenario: Concurrent sampled admission during promotion

- **WHEN** a matching sample is cataloged before the promotion transaction starts
- **THEN** promotion skips the duplicate and keeps the existing file, even at the archive destination

#### Scenario: A scan discovers a copy of a promoted archive

- **WHEN** proxy promotion publishes an archive and a scan later discovers identical bytes
- **THEN** the scan finds the promoted Media by its `sample1:` fingerprint

### Requirement: Export manifests verify complete originals

The photo export manifest SHALL record `sha256:` followed by the SHA-256 digest of the full
plaintext written for each original.

#### Scenario: Export of sampled content

- **WHEN** a photo export writes an original from a row with a sampled identity
- **THEN** the manifest digest verifies the complete exported bytes
