# web-unmatched-fix-flows Specification

## Purpose
Make Manage → Unmatched a place an operator can fix unmatched media quickly: group items by parent, explain why each failed in plain language, search from the parent's title and year, and apply one match to a whole group.

## Requirements

### Requirement: Unmatched rows are grouped and explained

The web Unmatched page SHALL load every page of the unmatched queue and show rows that share
plugin, scraper and best candidate (or best-candidate title when there is no candidate id) as one
group with an item count. Each group SHALL state in plain words why it did not match, without
showing raw scores as the explanation, and SHALL add a naming hint when the file path's parent
folder carries a year or a bracketed qualifier.

#### Scenario: Tied candidates for a ten-item group

- **WHEN** ten queue rows share a plugin, scraper and candidate and their reason is `ambiguous`
- **THEN** the page shows one group of ten that says several catalog entries fit equally well

#### Scenario: Qualifier in the folder name

- **WHEN** a group's file sits under a folder named `Name (US)` and its reason is
  `below-threshold`
- **THEN** the group says the best guess matched only that percentage and that the folder name
  carries a qualifier that can lower the match

### Requirement: A group is fixed in one step

The page SHALL prefill the search title and year from the parent folder name, load candidates when
a group opens, and preselect the candidate whose year equals the folder year. One action SHALL
apply the chosen candidate to every item in the group and report how many succeeded and which
failed.

#### Scenario: Match all

- **WHEN** a manager opens a ten-item group and chooses "Match all" with the preselected candidate
- **THEN** `fixMatch` runs for each of the ten items, progress is shown, and the queue reloads
  without them

#### Scenario: Partial failure

- **WHEN** one apply in the group fails
- **THEN** the rest still apply and the page names the item that failed

### Requirement: Fix-match search accepts a year

`fixMatchCandidates` SHALL accept an optional positive `year` variable. When present the search
SHALL pass it to the scraper's year slot and the candidates SHALL be scored with it as the parsed
year, so equal titles with different years no longer tie.

#### Scenario: Equal titles, different years

- **WHEN** two candidates share the query title, one from 2020 and one from 1968, and the request
  carries `year: 2020`
- **THEN** the 2020 candidate scores higher and is not marked ambiguous

### Requirement: Naming and hide actions are honest

The page SHALL show a group's file path and link to Manage → Libraries (folder rename) and
Manage → Rename (file rename). A group from a source with no catalog SHALL say so instead of
offering a search. Hide SHALL be available per item and per group and SHALL say the item can
return on the next scan. When the queue holds more rows than one load reads, the page SHALL say
it shows only the first rows.

#### Scenario: Source with no catalog

- **WHEN** a group's scraper is the manual-only source
- **THEN** the panel explains it cannot be searched and offers no search or candidate list

#### Scenario: Hide a group

- **WHEN** a manager hides a group
- **THEN** each item's queue row is dismissed and the confirmation said they may return on the
  next scan

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

### Requirement: Fix Match can read declared scraper child listings

Fix Match SHALL expose each searchable scraper's ordered child level names and whether it
declares child listings. Managers SHALL be able to read a declared level for visible Media
outside Encrypted Libraries. The read SHALL enforce the scraper's fetch controls and row limits.

#### Scenario: A manager opens a child level

- **WHEN** a manager requests the first level with a valid candidate ID
- **THEN** the read returns the scraper's mapped child rows
- **AND** a later level can be read only with a key returned at the previous level for that candidate

#### Scenario: A parent key appears under several earlier rows

- **WHEN** a later level's parent key was returned under more than one earlier row
- **AND** those rows substitute different values into the level's list template
- **THEN** the read rejects the request instead of choosing one of them

#### Scenario: A request supplies an invalid level or path value

- **WHEN** the scraper has no child listings, the requested level is absent, or a required path value is invalid
- **THEN** the read rejects the request

### Requirement: Scraper list paths follow the plugin SDK dialect

Search candidates and child listing rows SHALL support a root array and terminal filter-all
predicates, including row-relative predicate keys.

#### Scenario: A declared list path filters an array

- **WHEN** the scraper declares a terminal filter-all predicate
- **THEN** all matching rows are available for mapping in source order

### Requirement: Fix Match supports a manual ordinal

Managers SHALL be able to preview, set and clear an ordinal on visible Media supported by Fix Match.
The ordinal SHALL persist by Media ID and take precedence over the parser in identification lookups.
A supplied sequence SHALL have at most 32 characters and a supplied index SHALL be nonnegative.

#### Scenario: An operator previews and applies an ordinal

- **WHEN** the manager supplies a sequence or index to Fix Match preview or apply
- **THEN** the lookup uses the supplied ordinal, with an omitted sequence or index defaulting to empty or zero
- **AND** the Apply commits the ordinal with the replacement metadata and lists it among the item's locked values
- **AND** an Apply refused before that commit (a failed lookup, invalid input, or hydration the server cannot run) changes neither the ordinal nor the Media's Entity links
- **AND** Entity links follow the commit, so a retry after a failed link write converges

#### Scenario: An operator replaces a match or clears the ordinal

- **WHEN** the manager applies a match without an ordinal or lock selection
- **THEN** the same candidate keeps the saved ordinal and a different candidate clears it
- **AND** explicitly clearing the ordinal restores parser lookup, with a standalone clear preserving the fingerprint pin
