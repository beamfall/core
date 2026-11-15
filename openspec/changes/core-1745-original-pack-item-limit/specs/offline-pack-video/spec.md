## ADDED Requirements

### Requirement: Original quality refuses oversized video items up front

`createOfflinePack` SHALL refuse an Original-quality request, whether `original` is named or no
quality is given, when any video item it resolves to is larger than the 4 GiB per-item bound. The
refusal SHALL happen before any job is queued, SHALL carry the INVALID error code, and its message
SHALL name the 4 GiB limit and suggest a transcoded quality. An item of exactly 4 GiB SHALL be
admitted. Transcoded qualities SHALL NOT be refused on the source size.

#### Scenario: Original video over 4 GiB

- **WHEN** a pack is requested at `original` for a video item of 4 GiB plus one byte
- **THEN** the mutation fails with INVALID, the message names the 4 GiB limit, and no job is queued

#### Scenario: Transcoded quality of a large source

- **WHEN** the same video item is requested at `medium`
- **THEN** a background build is queued
