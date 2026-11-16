## ADDED Requirements

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
