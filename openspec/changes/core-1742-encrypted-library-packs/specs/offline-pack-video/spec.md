## MODIFIED Requirements

### Requirement: Download quality

`createOfflinePack` SHALL accept an optional `quality` of `original`, `high`, `medium` or `low`, and
SHALL refuse any other value before queueing a job. At `high`, `medium` and `low` the server SHALL
transcode each video item to an MP4 no taller than 1080, 720 and 480 lines respectively. Audio items
SHALL keep their original bytes at every quality.

#### Scenario: Packs from an encrypted Library

- **WHEN** the caller can see an unsealed encrypted Library
- **THEN** an `original` pack resolves and builds as in a public Library, sizing each item from its
  authenticated plaintext
- **AND** a `high`, `medium` or `low` pack builds without the plaintext source or transcode
  reaching disk

#### Scenario: Sealed or inaccessible Library

- **WHEN** a Library is sealed or the caller cannot see it
- **THEN** it contributes no items
- **AND** a build stops with no pack and no staged output if the Library is sealed or the caller
  loses access before the pack is written

#### Scenario: Unknown quality

- **WHEN** the request names quality `ultra`
- **THEN** the mutation fails and no job is queued

#### Scenario: Encrypted Library

- **WHEN** a `high`, `medium` or `low` pack is requested for a video item in an unsealed encrypted
  Library the caller can see
- **THEN** the item is transcoded from the decrypted source without plaintext reaching disk, and the
  pack is written with the transcode's size and digest

#### Scenario: Sealed during a build

- **WHEN** the encrypted Library is sealed, or the caller's Profile is disabled or loses the Library,
  while a reduced-quality pack is building
- **THEN** the transcode stops, the build fails, and no pack or staged output remains

#### Scenario: Original pack in an unsealed encrypted Library

- **WHEN** an `original` season or series pack is requested for a target in an unsealed encrypted
  Library the caller can see
- **THEN** the target resolves to the same Media it would in a public Library, and the pack is
  written with each item's plaintext size

#### Scenario: Sealed encrypted Library

- **WHEN** a pack is requested for a target in an encrypted Library that is sealed
- **THEN** the target resolves to no Media and no pack is written
