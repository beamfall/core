## MODIFIED Requirements

### Requirement: Download quality

`createOfflinePack` SHALL accept an optional `quality` of `original`, `high`, `medium` or `low`, and
SHALL refuse any other value before queueing a job. At `high`, `medium` and `low` the server SHALL
transcode each video item to an MP4 no taller than 1080, 720 and 480 lines respectively. Audio
items SHALL keep their original bytes at every quality. In a visible unsealed encrypted Library,
`high`, `medium` and `low` packs SHALL build without the plaintext source or transcode reaching
disk.

#### Scenario: Unknown quality

- **WHEN** the request names quality `ultra`
- **THEN** the mutation fails and no job is queued

#### Scenario: Encrypted Library

- **WHEN** a `high`, `medium` or `low` pack is requested for a video item in an unsealed encrypted
  Library the caller can see
- **THEN** the item is transcoded from the decrypted source without plaintext reaching disk, and the
  pack is written with the transcode's size and digest

## ADDED Requirements

### Requirement: Original packs in unsealed encrypted Libraries

In an unsealed encrypted Library the caller can see, an `original` pack SHALL resolve and build
as it does in a public Library, sizing each item from its authenticated plaintext.

#### Scenario: Original pack in an unsealed encrypted Library

- **WHEN** an `original` season or series pack is requested for a target in an unsealed encrypted
  Library the caller can see
- **THEN** the target resolves to the same Media it would in a public Library, and the pack is
  written with each item's plaintext size

### Requirement: Encrypted pack builds fail closed on lost access

A sealed encrypted Library, or one the caller cannot see, SHALL contribute no items to an offline
pack. A build SHALL stop with no pack and no staged output if the Library is sealed or the caller
loses access to it before the pack is written.

#### Scenario: Sealed during a build

- **WHEN** the encrypted Library is sealed, or the caller's Profile is disabled or loses the Library,
  while a reduced-quality pack is building
- **THEN** the transcode stops, the build fails, and no pack or staged output remains

#### Scenario: Sealed encrypted Library

- **WHEN** a pack is requested for a target in an encrypted Library that is sealed
- **THEN** the target resolves to no Media and no pack is written
