## ADDED Requirements

### Requirement: Job queue puts live work and failures first

The Console job queue SHALL list running, yielded and queued jobs first, then each failed job that
no later run of the same kind and folder finished cleanly, with its error text, then the remaining
history folded into one row per job kind (its newest run and a run count). Each row SHALL show when
the job was queued, started or finished as a relative time and, once started, how long it ran. On a
phone the progress text SHALL stay on one line beside a legible bar.

#### Scenario: Recurring polls

- **WHEN** the history holds hundreds of completed runs of one recurring job kind
- **THEN** the queue shows one history row for that kind with its run count

#### Scenario: A failure later cleared

- **WHEN** a scan of a folder failed and a later scan of the same folder completed
- **THEN** the failure is history, not an unresolved failure
