## ADDED Requirements

### Requirement: Library scans yield to live transcodes

A library scan job SHALL NOT start while a realtime transcode is active, and a scan running when
a realtime transcode begins SHALL stop and re-queue with its latest checkpoint and a bounded retry
delay instead of failing. Scan admission SHALL NOT consume the background-CPU generation budget.

#### Scenario: Transcode begins mid-scan

- **WHEN** a library scan is running and a live transcode starts
- **THEN** the scan yields with its latest checkpoint and re-queues after the retry delay

#### Scenario: Scan queued during playback

- **WHEN** a library scan is due while a live transcode is active
- **THEN** the scan does not run and re-queues with its checkpoint unchanged

#### Scenario: Idle server

- **WHEN** no transcode is active and the generation budget is full
- **THEN** a library scan is still admitted

### Requirement: Yield to playback is logged

The server SHALL log one Info line naming the job id and kind each time a running background job
is preempted by a live transcode.

#### Scenario: Preempted scan

- **WHEN** a running scan yields to a live transcode
- **THEN** the server logs `background job yielded to live playback` with the scan's job id
