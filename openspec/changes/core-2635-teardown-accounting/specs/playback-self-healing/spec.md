## ADDED Requirements

### Requirement: Encoder admission is independent of directory cleanup

Each encoder run SHALL own its CPU slot and governor lease. An attached
replacement SHALL transfer both to the next run. On detach, the server SHALL
release both as soon as encoder exit and reader revocation have both occurred.
The server SHALL drain endpoint work and fragment joins before removing the
directory and SHALL retain directory ownership and any disk reservations
until removal succeeds.

#### Scenario: Termination during replacement with a blocked source read

- **WHEN** a session is detached during a rendition replacement and the old
  encoder has exited and its readers have been revoked while an encrypted
  source read remains blocked
- **THEN** at capacity one an unrelated session can start before the read
  completes, termination returns while the starter owns teardown, and
  directory removal waits for the read to drain

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
