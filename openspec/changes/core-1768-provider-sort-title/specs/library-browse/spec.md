## ADDED Requirements

### Requirement: Title sort and letters key on the sort title

When an item has a sort title, the `libraryItems` title sort and its `letters` SHALL use it. When it
has none, they SHALL use the title. The sort title SHALL come from the item's metadata provider or
from an admin edit. Core SHALL NOT derive one from a language rule such as a leading-article list.

#### Scenario: Item with a sort title

- **WHEN** an item titled "The Quiet Orchard" has the sort title "Quiet Orchard, The"
- **THEN** a title sort places it among the "Q" items and `letters` counts it under `Q`

#### Scenario: Item without a sort title

- **WHEN** an item has no sort title
- **THEN** it sorts and is bucketed by its title

### Requirement: Providers supply sort titles below admin edits

When an identification or refresh result carries a sort title, it SHALL be stored at identification
rank, so a locked admin value is kept. When a later result carries none, the sort title an earlier
identification supplied SHALL be removed. A locked admin sort title SHALL be kept against any
provider result. An admin sort title whose lock was cleared SHALL be kept when a result carries no
sort title, and SHALL be replaced when a result carries one.

#### Scenario: Re-match to a candidate without a sort title

- **WHEN** an item's provider sort title came from one candidate and the item is re-matched to a
  candidate that has none
- **THEN** the item has no sort title and sorts by its new title

#### Scenario: Admin sort title survives a refresh

- **WHEN** an admin set the sort title, the lock is still set, and a refresh returns a different
  sort title or none
- **THEN** the admin's sort title is kept

#### Scenario: Unlocked admin sort title and a result without one

- **WHEN** an admin cleared the lock on their sort title and a refresh returns no sort title
- **THEN** the admin's sort title is kept
