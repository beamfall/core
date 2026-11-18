## ADDED Requirements

### Requirement: Library metadata refresh job

The server SHALL offer a durable `library-metadata-refresh` job that re-applies, for every live
Media in one Library, the remembered candidate through the same path as `refreshMediaMetadata`,
so locked fields, locked artwork slots and pinned matches keep their values.

#### Scenario: Locked field kept

- **WHEN** a Library item has a locked title and the job refreshes it
- **THEN** the item keeps its locked title and unlocked fields take the provider's values

#### Scenario: Item that cannot be refreshed

- **WHEN** an item has no remembered candidate, or its plugin or scraper is not available
- **THEN** the job skips the item, counts it as skipped and continues

### Requirement: Refresh job is paced, resumable and reports progress

The job SHALL pause between items, checkpoint by Media id and yield between small batches, and
SHALL report progress as items processed out of the Library's live item count.

#### Scenario: Progress

- **WHEN** the job has processed part of a Library
- **THEN** the job's progress reports the processed and total item counts with unit `items`

### Requirement: One refresh per Library at a time

A request to refresh a Library while a refresh of that Library is queued, running or yielded
SHALL return the active job instead of adding a second one.

#### Scenario: Second request

- **WHEN** an operator requests a refresh twice for the same Library
- **THEN** both requests return the same job id

### Requirement: Refresh can be canceled

Canceling the job SHALL stop it before the next item.

#### Scenario: Cancel mid-run

- **WHEN** the operator cancels a running refresh
- **THEN** the job ends canceled and no further items are refreshed

### Requirement: Refresh mutation authority

`refreshLibraryMetadata(libraryID)` SHALL queue the job and return it, and SHALL require an Admin
Session or an agent, cli or diagnostics API token, the same authority as `scanLibrary`. A Library
the caller cannot see SHALL be refused as unknown.

#### Scenario: Viewer refused

- **WHEN** a non-admin session calls `refreshLibraryMetadata`
- **THEN** the call is refused and no job is queued

### Requirement: Console Library card action

Each Library card in the Console SHALL offer "Refresh all metadata" behind a confirmation that
says the refresh may take a while, and on confirm SHALL queue the refresh and point the operator
to Operations.

#### Scenario: Confirm

- **WHEN** the operator confirms the refresh on a Library card
- **THEN** the Console calls `refreshLibraryMetadata` for that Library and says the job is queued
