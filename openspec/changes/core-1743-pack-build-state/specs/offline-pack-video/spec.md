## ADDED Requirements

### Requirement: Building packs are listed with progress and failure

The server SHALL persist the state of each background pack build under its pre-assigned PackID until
the pack is sealed. `offlinePacks` SHALL list the caller's builds with state `building` or `failed`,
a `progress` between 0 and 1, and for a failed build a `failureReason` from a closed set of codes,
before its sealed packs. A sealed pack SHALL report state `ready` and progress 1, and SHALL not be
listed twice. Build state SHALL survive a server restart and SHALL be visible only to its Profile.

#### Scenario: Building

- **WHEN** a quality pack has been requested and its job has not finished
- **THEN** `offlinePacks` lists the PackID with state `building` and progress below 1

#### Scenario: Ready

- **WHEN** the build seals the pack
- **THEN** `offlinePacks` lists the PackID once, with state `ready` and progress 1

#### Scenario: Failed

- **WHEN** the build fails because a source file is missing
- **THEN** `offlinePacks` lists the PackID with state `failed` and failure reason
  `source_unavailable`, and no pack is written

### Requirement: Failed pack builds can be retried by PackID

`retryOfflinePack(id)` SHALL restart a failed build of the caller's Profile and device under the
same PackID, target and quality, and queue one build job. It SHALL be idempotent: a retry of a build
that is already `building` SHALL queue nothing and return it, and a retry of a sealed pack SHALL
return the pack unchanged. `revokeOfflinePack` on a failed build SHALL dismiss it.

#### Scenario: Retry

- **WHEN** a failed build is retried twice after its source is restored
- **THEN** one job is queued, both responses carry the same PackID with state `building`, and the
  pack is then listed as `ready`

#### Scenario: Unknown or foreign ID

- **WHEN** the ID names no build or pack of the caller's Profile
- **THEN** the mutation fails with not found and nothing is queued
