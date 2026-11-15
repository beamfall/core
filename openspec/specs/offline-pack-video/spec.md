# offline-pack-video Specification

## Purpose
Extends offline packs from audio to video: a viewer can download a container's items at a chosen transcode quality and keep an automatic pack of the next unwatched items on the device.

## Requirements

### Requirement: Video Media in offline packs

An offline pack build SHALL accept audio and video Media and SHALL refuse a target that resolves to
any other Media class.

#### Scenario: Video target

- **WHEN** a target resolves to visible video Media
- **THEN** the build seals each item and writes a ready pack

#### Scenario: Image target

- **WHEN** a target resolves to an image
- **THEN** the build fails and no pack is written

### Requirement: Download quality

`createOfflinePack` SHALL accept an optional `quality` of `original`, `high`, `medium` or `low`, and
SHALL refuse any other value before queueing a job. At `high`, `medium` and `low` the server SHALL
transcode each video item to an MP4 no taller than 1080, 720 and 480 lines respectively. Audio
items SHALL keep their original bytes at every quality.

#### Scenario: Unknown quality

- **WHEN** the request names quality `ultra`
- **THEN** the mutation fails and no job is queued

#### Scenario: Encrypted Library

- **WHEN** a non-original quality is requested for an item in an encrypted Library
- **THEN** the build fails and no pack is written

### Requirement: Transcoded items are sealed as transcoded

A transcoded item SHALL be hashed and sealed from the transcoded bytes, so its manifest size and
digest describe them. The per-item bound and the build byte budget SHALL apply to the bytes
actually sealed, and temporary transcodes SHALL not remain in the pack directory.

#### Scenario: Medium quality

- **WHEN** a video target is requested at `medium`
- **THEN** every sealed video item is the transcoded MP4 and its manifest size and digest match it

#### Scenario: Transcode over the item bound

- **WHEN** a transcoded item is larger than the per-item bound
- **THEN** the build fails and the pack directory is removed

### Requirement: Background pack builds

A `createOfflinePack` request that names a `quality` SHALL be built by a background job and SHALL
return at once with the pack's ID and state `building`. When the build succeeds, the pack SHALL be
listed in `offlinePacks` under that ID. A repeat request for the same Profile, target and quality
while the build is active SHALL return the same ID.

#### Scenario: Repeat request

- **WHEN** the same Profile requests the same target at the same quality twice before the first
  build runs
- **THEN** both responses carry the same pack ID and one build job exists

### Requirement: Profile-aware target resolution

The pack service SHALL call a resolver's `ResolveForProfile` with the requesting Profile when the
resolver implements it, and `Resolve` otherwise.

#### Scenario: Watch-state target

- **WHEN** a target kind's resolver depends on what the Profile has finished
- **THEN** the build resolves it with the requesting Profile's watch state
