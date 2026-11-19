## ADDED Requirements

### Requirement: Encoder admission is independent of directory cleanup

For a detached session, the server SHALL release its CPU transcode slot and
governor transcode lease once the encoder has stopped and its readers have been
revoked. The server SHALL drain in-progress fragment joins before removing the
directory and SHALL retain directory ownership and any disk-space reservations
until directory removal succeeds, including across failed attempts.

#### Scenario: Fragment join is delayed at the transcode cap

- **WHEN** a foreground request preempts a session at capacity and the victim's
  encoder has stopped and its readers have been revoked while a fragment join
  is still in progress
- **THEN** the foreground encoder can start, and the victim's directory is
  removed only after the join drains and before shutdown completes

#### Scenario: Directory removal is delayed after the last encoder stops

- **WHEN** directory removal is blocked after the last encoder has stopped and
  its readers have been revoked
- **THEN** the governor admits eligible background generation during directory
  cleanup

#### Scenario: Directory removal must be retried

- **WHEN** removal of a detached session's directory fails
- **THEN** the orphan purge skips the directory both while retry is pending and
  while a retry is running
