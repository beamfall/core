# playback-rendition-cache Specification

## Purpose
Defines the cached transcode renditions playback serves: how each rendition generation is tracked, and how abandoned or stale generations are retired.

## Requirements

### Requirement: Abandoned rendition generations are marked failed

The server SHALL mark `failed` every precomputed playback rendition row left `generating` by a run
that is no longer running: at server start before job workers resume, and for an encrypted Library
each time it is Unsealed. It SHALL NOT change the row of a generation that a run in the current
server process is still producing.

#### Scenario: Server stopped mid-run

- **WHEN** the server stops while a rendition is generating and is then started again
- **THEN** that rendition is reported as failed, not generating, before any job resumes

#### Scenario: Run aborted while its Library was Sealed

- **WHEN** a rendition run for an encrypted Library aborts while the Library is Sealed and the
  Library is then Unsealed
- **THEN** that rendition is reported as failed

#### Scenario: A run in flight is not clobbered

- **WHEN** a rendition run is still in progress in the current server process during the sweep
- **THEN** its rendition stays generating and the run can still publish it

### Requirement: Each Library has a rendition precompute Schedule that ships disabled

When a rendition cache budget is configured, the server SHALL declare one rendition precompute
Schedule per Library, SHALL seed it disabled, and SHALL NOT enable it on its own at any later
reconcile. Only the operator enabling it starts precompute for that Library.

#### Scenario: A new Schedule is seeded disabled

- **WHEN** the server reconciles Schedules for a Library that has no precompute Schedule yet
- **THEN** the Schedule is created disabled and queues no work when it comes due

#### Scenario: Reconcile keeps the operator's choice

- **WHEN** the server reconciles again, restarts, or the Library's Schedule is orphaned and
  declared again
- **THEN** a Schedule the operator never enabled stays disabled, and one the operator enabled
  stays enabled

### Requirement: An enabled Library precompute Schedule stays inside the cache budget

An enabled Library precompute Schedule SHALL queue Background precompute jobs only for that
Library's renditions that are not already ready or generating, SHALL stop planning once the ready
cache plus the planned renditions' estimated sizes would exceed the cache budget, and SHALL queue
nothing for an Encrypted Library the server holds no key for.

#### Scenario: The Library's missing renditions are queued

- **WHEN** an enabled Schedule runs for a Library with Media whose renditions are not built
- **THEN** one Background precompute job is queued per missing rendition, and ready renditions are
  not queued again

#### Scenario: The budget bounds a run

- **WHEN** the ready cache plus the next rendition's estimate exceeds the budget
- **THEN** the run queues no further renditions

#### Scenario: An Encrypted Library without its key

- **WHEN** the Schedule runs for an Encrypted Library that is Sealed or cannot be generated on
- **THEN** no precompute job is queued
