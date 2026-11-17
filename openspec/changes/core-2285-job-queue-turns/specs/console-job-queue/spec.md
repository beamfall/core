## ADDED Requirements

### Requirement: Jobs of one priority take turns

Among runnable jobs of the same priority, the server SHALL run the job that has waited longest.
A queued job SHALL count as waiting since it was created and a yielded job since it last yielded.

#### Scenario: A long job yields while a newer job is queued

- **WHEN** a running job yields and a job of the same priority was queued before that yield
- **THEN** the queued job is claimed next, and the yielded job is claimed after it

### Requirement: Finished jobs are removed

The daily events compaction SHALL remove completed and canceled jobs that finished more than
24 hours ago and failed jobs that finished more than 7 days ago, together with their checkpoint
and job events. It SHALL NOT remove a queued, running or yielded job.

#### Scenario: A job completed two days ago

- **WHEN** the events compaction runs and a job completed two days ago
- **THEN** the `jobs` query no longer returns that job

#### Scenario: A job failed two days ago

- **WHEN** the events compaction runs and a job failed two days ago
- **THEN** the `jobs` query still returns that job with its error
