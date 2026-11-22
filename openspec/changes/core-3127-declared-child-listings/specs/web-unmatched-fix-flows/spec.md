## ADDED Requirements

### Requirement: Fix Match can read declared scraper child listings

Fix Match SHALL expose each searchable scraper's ordered child level names and whether it
declares child listings. Managers SHALL be able to read a declared level for visible Media
outside Encrypted Libraries. The read SHALL enforce the scraper's fetch controls and row limits.

#### Scenario: A manager opens a child level

- **WHEN** a manager requests the first level with a valid candidate ID
- **THEN** the read returns the scraper's mapped child rows
- **AND** a later level can be read only with a key returned at the previous level for that candidate

#### Scenario: A request supplies an invalid level or path value

- **WHEN** the scraper has no child listings, the requested level is absent, or a required path value is invalid
- **THEN** the read rejects the request

### Requirement: Scraper list paths follow the plugin SDK dialect

Search candidates and child listing rows SHALL support a root array and terminal filter-all
predicates, including row-relative predicate keys.

#### Scenario: A declared list path filters an array

- **WHEN** the scraper declares a terminal filter-all predicate
- **THEN** all matching rows are available for mapping in source order
