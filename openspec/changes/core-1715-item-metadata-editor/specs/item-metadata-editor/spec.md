## ADDED Requirements

### Requirement: Admins edit an item's details and every edit locks the field

The server SHALL let an admin edit one Media item's title, sort title, overview, year,
plugin-declared fields and artwork. Each edit SHALL be written at manual rank and SHALL lock the
edited field, so identification, refresh, rescans and plugins writing at a lower rank leave it
unchanged until an admin unlocks it. A title SHALL NOT be empty and a year SHALL be a whole number
from 0 to 9999. Non-admin callers and Media hidden from the caller SHALL be refused.

#### Scenario: Editing the title locks it

- **WHEN** an admin sets an item's title and sort title
- **THEN** the item reads the new values and both fields report locked

#### Scenario: Invalid year

- **WHEN** an admin sets a year of 12345
- **THEN** the edit is refused and nothing changes

#### Scenario: Viewer cannot edit

- **WHEN** a viewer calls the edit
- **THEN** it is refused

### Requirement: Sort title orders library browse

A Media item MAY carry a sort title. Library browse and its letter rail SHALL order by the sort title
when one is set and by the title otherwise.

#### Scenario: Sort title moves an item

- **WHEN** an item titled "The Example" is given the sort title "Example, The"
- **THEN** library browse orders it under E

### Requirement: Per-item refresh re-runs the provider without overwriting locked fields

The server SHALL remember the candidate that identified a Media item. Refreshing the item SHALL
re-apply that candidate through the identify path below manual rank, so unlocked fields take the
provider's values and locked fields, including locked artwork slots, keep theirs. Refreshing an
item with no remembered candidate SHALL be refused and SHALL point the admin to Fix Match.

#### Scenario: Locked title survives refresh

- **WHEN** an admin edits an identified item's title and summary, unlocks the summary, and refreshes
- **THEN** the title keeps the admin's value and the summary returns to the provider's value

#### Scenario: Unlocking lets refresh win

- **WHEN** the admin unlocks the title and refreshes again
- **THEN** the title returns to the provider's value

#### Scenario: Locked artwork slot

- **WHEN** an item's backdrop slot is locked and identification imports artwork
- **THEN** only the unlocked slots change

#### Scenario: Unidentified item

- **WHEN** an admin refreshes an item that was never identified
- **THEN** the refresh is refused with a pointer to Fix Match

### Requirement: Web Manage per-item editor

The web console SHALL offer a Manage page for one item, reachable by media ID and from collection
members, that edits the details, plugin-declared fields and artwork, shows each field's lock state
with a lock toggle, sends only the fields that changed, and offers Refresh only when the item can be
refreshed.

#### Scenario: Save sends only changed fields

- **WHEN** an admin changes the title and sort title and saves
- **THEN** only those two fields are sent and both show as locked

#### Scenario: Refresh unavailable

- **WHEN** the item has not been identified
- **THEN** Refresh is disabled and the page points to Fix Match
