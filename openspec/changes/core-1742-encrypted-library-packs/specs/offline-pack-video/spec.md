## MODIFIED Requirements

### Requirement: Download quality

`createOfflinePack` SHALL accept an optional `quality` of `original`, `high`, `medium` or `low`, and
SHALL refuse any other value before queueing a job. At `high`, `medium` and `low` the server SHALL
transcode each video item to an MP4 no taller than 1080, 720 and 480 lines respectively. Audio
items SHALL keep their original bytes at every quality. In an unsealed encrypted Library the caller
can see, an `original` pack SHALL resolve and build as it does in a public Library, sizing each item
from its authenticated plaintext. A sealed Library, or one the caller cannot see, SHALL contribute
no items.

#### Scenario: Unknown quality

- **WHEN** the request names quality `ultra`
- **THEN** the mutation fails and no job is queued

#### Scenario: Encrypted Library

- **WHEN** a non-original quality is requested for an item in an encrypted Library
- **THEN** the build fails and no pack is written

#### Scenario: Original pack in an unsealed encrypted Library

- **WHEN** an `original` season or series pack is requested for a target in an unsealed encrypted
  Library the caller can see
- **THEN** the target resolves to the same Media it would in a public Library, and the pack is
  written with each item's plaintext size

#### Scenario: Sealed encrypted Library

- **WHEN** a pack is requested for a target in an encrypted Library that is sealed
- **THEN** the target resolves to no Media and no pack is written
