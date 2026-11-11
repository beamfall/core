# store-integrity Specification

## Purpose
Integrity rules of the store: one identity per managed file's content, valid JSON in queued writes, and accurate not-found reporting.

## Requirements

### Requirement: Managed file identity comes from file content

Managed import and managed convert SHALL fingerprint each admitted file with a SHA-256 digest of its
whole content, and SHALL NOT derive the fingerprint from the file's path.

Managed import and managed convert SHALL refuse, with a validation error, a file whose content
fingerprint is cataloged, live or missing (including trashed), at a different path or in a different library, and
SHALL leave the existing media file unchanged.

#### Scenario: Same bytes at two paths

- **WHEN** a file is imported and a second file with identical content is then imported into the
  same plaintext library at a different path
- **THEN** the second import fails with a validation error, no file is left at the second path, and
  the first media file keeps its path, library and Media

#### Scenario: Same bytes as a trashed file

- **WHEN** a managed file is deleted to managed trash and a file with identical content is then
  imported at a different path
- **THEN** the import fails with a validation error, no file is left at the new path, and restoring
  the trashed file revives its original media file at its original path

#### Scenario: New bytes at a reused path

- **WHEN** a managed file is deleted and a file with different content is imported at the same path
- **THEN** the new media file carries a different content fingerprint and a different Media

#### Scenario: Converted artifact

- **WHEN** managed convert admits a converted artifact
- **THEN** the artifact's media file carries the SHA-256 digest of the artifact's content

### Requirement: Job and outbox JSON is validated before it is stored

The store SHALL reject a job payload, job checkpoint, job event payload or outbox payload that is
not valid JSON with a validation error, and SHALL NOT persist it. An empty payload SHALL continue to
be stored as `{}`.

#### Scenario: Malformed job payload

- **WHEN** a job is enqueued with a payload that is not valid JSON
- **THEN** the call returns a validation error and no job is stored

#### Scenario: Malformed checkpoint or event

- **WHEN** a job checkpoint, job event or outbox payload that is not valid JSON is written
- **THEN** the call returns a validation error and nothing is stored

### Requirement: Not-found is reported only for missing rows

Store reads SHALL return a not-found error only when the row does not exist, and SHALL return any
other database error to the caller. A failed lookup SHALL NOT be treated as an unrestricted library.

#### Scenario: Database failure during a saved filter read

- **WHEN** reading a saved filter or smart collection fails for a reason other than a missing row
- **THEN** the call returns that error, not the not-found error

#### Scenario: Database failure during restriction annotation

- **WHEN** looking up the libraries of source items fails
- **THEN** the read returns the error instead of marking the items unrestricted

### Requirement: Entity merge undo reverses only the merged pair

`MergeEntities` SHALL journal the winner's Entity id with the loser's item. Undo of an entity merge
SHALL restore the journaled relation rows of that loser and that winner and un-trash the loser. It
SHALL NOT infer the winner from relation rows, and SHALL NOT alter relation rows that touch
neither the loser nor the winner.

#### Scenario: Loser points at an unrelated Entity

- **WHEN** Entity L relates to Entity T, T holds relations to Media that involve neither L nor the
  winner W, L is merged into W, and the merge batch is undone
- **THEN** every relation row is as it was before the merge, T's relations are unchanged, and L is
  live again

### Requirement: Entity merge undo refuses journals without a winner

Undo of an entity merge journal item that has no winner id SHALL fail with a validation error and
SHALL change nothing, leaving the batch not undone.

#### Scenario: Journal written without the winner id

- **WHEN** an entity merge batch whose journal item has no winner id is undone
- **THEN** the undo fails with a validation error, no relation row changes, the loser stays
  trashed, and the batch is not marked undone
