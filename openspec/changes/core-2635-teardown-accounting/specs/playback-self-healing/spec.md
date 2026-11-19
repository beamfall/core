## ADDED Requirements

### Requirement: Directory cleanup releases its lease after the transcode slot

For a detached session, the server SHALL release its transcode slot after the
encoder has stopped and its readers and in-progress fragment joins have drained.
Directory cleanup SHALL proceed independently of slot admission. If the detached
session holds a lease, the server SHALL retain it until directory removal succeeds,
including across failed removal attempts.

#### Scenario: Directory removal is delayed at the transcode cap

- **WHEN** a detached session has stopped its encoder and drained its readers and
  joins, and its directory removal is blocked
- **THEN** another encoder can take the released transcode slot while the detached
  session's lease remains held until its directory is removed

#### Scenario: Directory removal must be retried

- **WHEN** removal of a detached session's directory fails
- **THEN** the detached session's lease remains held and is released after a
  successful retry
