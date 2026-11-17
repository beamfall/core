# console-job-queue Specification

## Purpose
The Console Job queue tells an operator what each background job does and which Library folder it
works on, and lets them force a Library rescan.

## Requirements

### Requirement: Job target Library

The `jobs` query SHALL report, for a job whose payload names a Library, that Library's id, title,
the folder root and whether the scan is a backfill, but only when the session may see that
Library's operational detail.

#### Scenario: Visible library scan

- **WHEN** a manager lists jobs and a scan job targets a visible, unrestricted Library folder
- **THEN** the job carries `target` with the Library id, title and folder root

#### Scenario: Restricted library before reveal

- **WHEN** a session that has not revealed restricted Libraries lists jobs that target a
  restricted Library
- **THEN** the job carries no `target` and the response contains no folder path of that Library

### Requirement: Job purpose in the Console

The Console Job queue SHALL show for each job a label and a one-sentence description of what that
job kind does, and for a job with a target the Library title and folder.

#### Scenario: Scan row

- **WHEN** the queue shows a scan job with a target
- **THEN** the row says that it walks a library folder and imports media, and shows
  "Library title · folder"

#### Scenario: Unknown kind

- **WHEN** the queue shows a job kind the client does not know
- **THEN** the row shows a humanized kind label and no description

### Requirement: Force a scan from the job queue

The Console Job queue SHALL offer a control that queues a scan of every library folder.

#### Scenario: Scan now

- **WHEN** the operator presses the scan control
- **THEN** a scan is requested for every root of every Library that has roots

### Requirement: Scan job result summary

A completed scan job SHALL record how many files it checked, added, changed, marked missing and
left unmatched, as a `scan-summary` job event, and the Console SHALL show those counts for a
finished rescan with a route to the Fix match view when any item needs a match.

#### Scenario: Rescan adds files and leaves one unmatched

- **WHEN** a rescan finds new files and one of them cannot be matched automatically
- **THEN** the scan job records a `scan-summary` event with the added and unmatched counts
- **AND** the Console rescan status shows the checked, added and need-a-match counts
- **AND** it links to the Fix match view at `/manage/unmatched`

#### Scenario: Rescan with nothing new

- **WHEN** a rescan finds no added, changed, missing or unmatched files
- **THEN** the Console rescan status shows only how many files it checked and no Fix match link

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

### Requirement: A yield is stamped when it happens

A job transition (yield, failure, completion) SHALL record the later of the job's last update and
the current time, never the time the job was claimed. This keeps the existing requirement "Jobs
of one priority take turns" true for jobs that run longer than one claim.

#### Scenario: Job queued during a long run

- **WHEN** a job runs for several minutes, a job of the same priority is queued during the run,
  and the first job then yields
- **THEN** the queued job is claimed before the yielded one

### Requirement: Scans yield on time as well as on files

A scan job SHALL yield after two minutes of work even when it has not finished its file batch. The
two minutes SHALL count from the run's first checkpoint, so the walk a resumed run makes back to its
resume point does not spend them.

#### Scenario: Slow files

- **WHEN** a scan's time budget is spent before its file batch is done
- **THEN** the job yields and a later claim resumes where it stopped

#### Scenario: Slow walk back to the resume point

- **WHEN** a resumed scan takes longer than its time budget to walk back to its checkpoint
- **THEN** the run still checkpoints new files for its whole budget before it yields

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

### Requirement: A resumed scan seeks to its checkpoint

A resumed scan SHALL NOT list or stat a folder whose whole subtree sorts before its checkpoint in
walk order, and SHALL visit the same files after the checkpoint as a full walk of the same tree.

#### Scenario: Folders before the checkpoint are not read

- **WHEN** a scan resumes from a checkpoint inside a Library folder
- **THEN** it reads only the checkpoint's ancestor folders and the folders after them, and imports
  the files after the checkpoint in the same order as a full walk

#### Scenario: Files deleted after the checkpoint

- **WHEN** a resumed scan finds no media files after its checkpoint because they were deleted
- **THEN** it still marks them missing rather than treating the root as empty

### Requirement: Folder preflight probes share the worker budget

When a Library's parser reads a folder's files together, the scan SHALL probe that folder's files
within the scan's worker budget, in parallel up to that budget, and SHALL decide the folder as if
they were probed one at a time.

#### Scenario: Several workers

- **WHEN** a scan with several workers reaches a folder of files its parser reads together
- **THEN** it probes up to that many files at once and never more probes than workers in total
