## ADDED Requirements

### Requirement: Live playback cutoff

The server SHALL bind live HLS access to the exact playback instance before scoped verification, and SHALL fence subsequent artifact access and playback process permits when it revokes that instance.

#### Scenario: Termination during preparation

- **WHEN** a live playback instance is revoked before its process permit is granted
- **THEN** the instance cannot start a playback process and its bound requests receive an eviction result

#### Scenario: Termination after encoder handoff

- **WHEN** termination revokes an instance with a handed-off encoder
- **THEN** termination joins its reap and the instance releases or transfers its capacity at reap

### Requirement: Source-qualified playback results

The server SHALL qualify live setup and in-memory source observations by verified source identity and apply tempo to source duration when committing the output shape.

#### Scenario: Source replacement during preparation

- **WHEN** the source generation changes before a prepared playback shape can commit
- **THEN** the server declines the stale result without serving artifacts from it
