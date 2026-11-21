## ADDED Requirements

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

The page SHALL show a group's file path, link to Manage → Rename and offer to rescan the group's
items. Hide SHALL be available per item and per group and SHALL say the item can return on the
next scan.

#### Scenario: Hide a group

- **WHEN** a manager hides a group
- **THEN** each item's queue row is dismissed and the confirmation said they may return on the
  next scan
