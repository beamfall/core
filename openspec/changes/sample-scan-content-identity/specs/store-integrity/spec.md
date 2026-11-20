## MODIFIED Requirements

### Requirement: Managed file identity comes from file content

Managed staging SHALL compute both digests and store `sample1:` on new rows. Either digest matching
a row at another path or Library SHALL fail validation and leave the existing file and Media intact,
including missing or trashed rows. The first scan SHALL reuse the identity, record stats and OSHash
even if probing fails, and attempt stream and attachment inventory.

#### Scenario: Same bytes at two paths

- **WHEN** bytes cataloged with a sampled or legacy fingerprint are imported at a second path
- **THEN** the import fails with a validation error and leaves no file at the second path

#### Scenario: Same bytes as a trashed file

- **WHEN** a managed file is deleted to managed trash and identical content is imported
- **THEN** the import fails with a validation error and restoring revives the original Media

#### Scenario: New bytes at a reused path

- **WHEN** a different sampled identity is admitted at a deleted file's path
- **THEN** the replacement gets a different Media

#### Scenario: Converted artifact

- **WHEN** managed convert admits a converted artifact
- **THEN** its row carries the sampled identity of the artifact's bytes

#### Scenario: Managed reimport at an existing legacy path

- **WHEN** the staged full digest matches a legacy row at the same path and Library after external removal
- **THEN** admission preserves the legacy fingerprint, Media ID, watch state, and match pins, including
  for missing files and soft-trashed Media, which retain their trash timestamp and remain restorable
- **AND** it keeps matching stats and clears stale stats for the next scan to verify and record once

## ADDED Requirements

### Requirement: Scans bound the reads used for content identity

`sample1:` SHALL prefix the lowercase SHA-256 hex digest of the unsigned 8-byte big-endian size
followed by the sampled bytes. Files up to 917,504 bytes SHALL contribute all bytes. Larger files
SHALL contribute 14 chunks of 65,536 bytes at floor(i × (size − 65,536) / 13) in order for i = 0…13.
Deduplication and moves SHALL compare exact fingerprints. Equal sizes and samples share an identity.
Mtime comparisons SHALL round Unix nanoseconds toward zero to milliseconds.

#### Scenario: First scan of a large file

- **WHEN** a new large file is scanned
- **THEN** fingerprinting reads 917,504 bytes and duplicate samples resolve to one file identity

#### Scenario: Unchanged legacy file

- **WHEN** the path, recorded size and recorded mtime match a legacy row
- **THEN** the existing public catalog fast path reuses the legacy identity without reading bytes for fingerprinting

#### Scenario: Legacy file known only to an encrypted Library

- **WHEN** a legacy fingerprint exists only in an encrypted catalog
- **THEN** the scan retains the existing public catalog routing and probe behavior
- **AND** a legacy identity can be replaced by a sampled identity, including if both catalogs share the path

#### Scenario: Legacy file at the same path with incomplete stats

- **WHEN** the public catalog lookup finds an available legacy row with an unrecorded size or mtime
  and its recorded stats still match
- **THEN** the scan compares the full digest once at that path, the sole full-digest scan case
- **AND** a match keeps the identity and records stats that survive reopening, including a verified
  Unix-epoch mtime, while a mismatch uses the sampled identity

#### Scenario: A scan cannot finish sampling a file

- **WHEN** a scan sees a file inside the stability window or any step of sampling fails
- **THEN** it skips admission and records the path as seen for the missing-file sweep
- **AND** the next scan can retry against the same stored identity and stats

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

Proxy promotion and qBittorrent ingest SHALL check both digests among live public files, excluding
trashed Media and Libraries. Promotion SHALL recheck in its publication transaction and verify
ownership with a bounded lookup before keeping a duplicate archive. The opened tee SHALL match
the completed tee's inode, size and mtime before and after fingerprinting. Ingest SHALL reject a
changed payload before lookup or relocation. Proxy archives SHALL use the sampled identity.

#### Scenario: Duplicate in either scheme

- **WHEN** either digest matches a live row, including a sample with different unsampled bytes
- **THEN** the existing Media and file remain intact, without publishing another file

#### Scenario: Concurrent sampled admission during promotion

- **WHEN** a matching sample is cataloged before the promotion transaction starts
- **THEN** promotion skips the duplicate and keeps the existing file, even at the archive destination

### Requirement: Export manifests verify complete originals

The photo export manifest SHALL record `sha256:` followed by the SHA-256 digest of the full
plaintext written for each original.

#### Scenario: Export of sampled content

- **WHEN** a photo export writes an original from a row with a sampled identity
- **THEN** the manifest digest verifies the complete exported bytes
