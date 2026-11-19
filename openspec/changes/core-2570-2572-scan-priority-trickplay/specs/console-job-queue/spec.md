## MODIFIED Requirements

### Requirement: Job queue puts live work and failures first

The Console job queue SHALL list running, yielded and queued jobs first, then each failed job that
no later run of the same kind and folder finished cleanly, with its error text, then the remaining
history folded into one row per job kind (its newest run and a run count). Each row SHALL show when
the job was queued, started or finished as a relative time and, once started, its elapsed duration.
Running jobs SHALL label elapsed time as
"{duration} since first start", including waits and yields. Running and yielded jobs with more
than one attempt SHALL append "attempt {attempts}". Yielded jobs SHALL keep their paused note
without a duration. Finished jobs SHALL say "took {duration}" for at most one attempt and
"took {duration} over {attempts} attempts" for multiple attempts. On a phone the progress text SHALL stay on one line beside a legible bar.

#### Scenario: Recurring polls

- **WHEN** the history holds hundreds of completed runs of one recurring job kind
- **THEN** the queue shows one history row for that kind with its run count

#### Scenario: A failure later cleared

- **WHEN** a scan of a folder failed and a later scan of the same folder completed
- **THEN** the failure is history, not an unresolved failure

#### Scenario: Running third attempt

- **WHEN** a running job has three attempts
- **THEN** the row says "{duration} since first start" and "attempt 3"

#### Scenario: Yielded repeated attempt

- **WHEN** a yielded job has three attempts
- **THEN** the row shows its paused note and "attempt 3", without a running duration

#### Scenario: Completed single attempt

- **WHEN** a job completes on its first attempt
- **THEN** the row says "took {duration}"

#### Scenario: Completed repeated attempt

- **WHEN** a job completes after two attempts
- **THEN** the row says "took {duration} over 2 attempts"

## ADDED Requirements

### Requirement: Scans are queued above background sweeps

New library scan jobs SHALL have priority 5, above background sweeps at 0 and below foreground
and playback demand at 10. Jobs of the same priority SHALL retain their existing turn-taking.
Deduplicating an active job SHALL preserve its existing priority.

#### Scenario: Scan and sweep are runnable

- **WHEN** a scan and a background sweep are both runnable
- **THEN** the scan is claimed first

#### Scenario: Foreground work is runnable

- **WHEN** priority-10 foreground work and a scan are both runnable
- **THEN** the foreground work is claimed first
