## ADDED Requirements

### Requirement: Purging an encrypted Library removes its encrypted derivatives

When a deleted encrypted Library is purged, the server SHALL remove that Library's regenerable
encrypted derivative caches: its precomputed rendition ciphertext and its encrypted subtitle cache.
If a removal fails, the server SHALL retry it on the next retention sweep or server start. Moving a
Library to the trash SHALL NOT remove its derivatives.

#### Scenario: Purge removes the derivative caches

- **WHEN** an admin purges a deleted encrypted Library that has precomputed renditions and cached
  subtitles
- **THEN** the Library's precomputed rendition and encrypted subtitle directories no longer exist

#### Scenario: A failed derivative removal is retried

- **WHEN** removing a purged encrypted Library's derivative caches fails
- **THEN** the Library stays purged, its keys are erased, and the next retention sweep or server
  start removes the caches

#### Scenario: Trash keeps derivatives

- **WHEN** an admin deletes an encrypted Library without purging it
- **THEN** its derivative caches are unchanged and restoring the Library needs no regeneration
