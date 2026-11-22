## ADDED Requirements

### Requirement: Fix Match reads are available for visible Media

Managers SHALL be able to read the current match, searchable scrapers and locked field values
for visible Media outside Encrypted Libraries. A recorded searchable scraper from the owning plugin
SHALL come first, followed by the remaining scrapers in declaration order. Identification
provenance lives in the shared catalog, so Fix Match reads and applies for Media in an Encrypted
Library SHALL be refused with the localized `fix_match.encrypted_library` error.

#### Scenario: A manager opens Fix Match for an identified item

- **WHEN** a manager opens Fix Match for visible Media with a recorded match
- **THEN** the current match includes its recorded source and candidate, and the stored title and year
- **AND** the first searchable scraper is the sole default when at least one exists
- **AND** the manager can read the item's manually locked values

#### Scenario: A manager opens Fix Match in an Encrypted Library

- **WHEN** a manager opens Fix Match for visible Media in an Encrypted Library
- **THEN** the request is refused with the localized `fix_match.encrypted_library` error
- **AND** no match, scraper or lock data is returned

### Requirement: Applying a replacement respects field locks

Fix Match SHALL work without an Unmatched row, pin the item's file fingerprints and preserve
manually locked fields unless the request explicitly clears their locks.

#### Scenario: The operator chooses which locks to clear

- **WHEN** the manager applies a replacement and names valid fields in clearLocks
- **THEN** the named locks are cleared for the replacement
- **AND** every other manual lock is kept
