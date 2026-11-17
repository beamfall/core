## ADDED Requirements

### Requirement: A yield is stamped when it happens

A job transition (yield, failure, completion) SHALL record the later of the job's last update and
the current time, never the time the job was claimed. This keeps the existing requirement "Jobs
of one priority take turns" true for jobs that run longer than one claim.

#### Scenario: Job queued during a long run

- **WHEN** a job runs for several minutes, a job of the same priority is queued during the run,
  and the first job then yields
- **THEN** the queued job is claimed before the yielded one

### Requirement: Scans yield on time as well as on files

A scan job SHALL yield after two minutes of work even when it has not finished its file batch.

#### Scenario: Slow files

- **WHEN** a scan's time budget is spent before its file batch is done
- **THEN** the job yields and a later claim resumes where it stopped

### Requirement: Job events are bounded

A job SHALL keep only its latest progress event. `jobEvents(id, limit, beforeID)` SHALL return at
most `limit` events (default 200, at most 1000): the newest ones, in the order they happened.
`beforeID` SHALL return the events before that event.

#### Scenario: Long scan

- **WHEN** a job reports progress many times
- **THEN** `jobEvents` holds one progress event for it

#### Scenario: Paging back

- **WHEN** a client reads `jobEvents` with `limit: 2` and then with `beforeID` set to the first
  returned id
- **THEN** the second page holds the two events before the first page

#### Scenario: Bad cursor

- **WHEN** `beforeID` is not a job event ID
- **THEN** the read fails with a client error

### Requirement: A scan of a missing root fails fast

A scan job whose library root is missing or unreadable SHALL fail without walking. It SHALL emit a
`scan-root-unavailable` job event with `libraryID`, `root` and `reason`, where reason is
`missing` or `unreadable`.

#### Scenario: Root removed

- **WHEN** a library's root directory does not exist and its scan runs
- **THEN** the job fails and its events include `scan-root-unavailable` with reason `missing`
