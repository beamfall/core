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
