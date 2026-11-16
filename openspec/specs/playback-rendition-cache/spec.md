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
