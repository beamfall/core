# scan-retention Specification

## Purpose
Defines when a library rescan may retire existing Media. Retirement needs evidence that the source is gone; a narrower plugin declaration only changes what is scanned, and never authorizes trashing Media that was already catalogued.

## Requirements

### Requirement: Plugin declarations do not authorize retirement
Core SHALL NOT trash existing Media because a plugin declaration excludes their class. When existing Media has a class absent from the current nonempty declaration, Core SHALL skip the scan missing pass conservatively.

#### Scenario: A declaration drops a class
- **GIVEN** a Library contains image Media and its plugin declares only video
- **WHEN** a rescan completes
- **THEN** the existing image remains present and untrashed
- **AND** new files still follow the current import declaration
