## ADDED Requirements

### Requirement: Source items report the acting Profile's playback state

Every operation that returns `SourceItem` for a Profile SHALL include an `engagement` object on each
item the Profile has playback state for, and SHALL omit it otherwise.

#### Scenario: Partly played item

- **WHEN** the Profile has played an item past the resume floor without finishing it
- **THEN** its `engagement` carries `positionSeconds` and `durationSeconds` and no `completed`

#### Scenario: Finished item

- **WHEN** the Profile has finished an item
- **THEN** its `engagement` carries `completed: true`

#### Scenario: Ordered container

- **WHEN** an entity has ordered children the scope can see
- **THEN** its `engagement` carries `totalCount` and `remainingCount` over those children only, and
  `completed: true` once none remain

#### Scenario: Hidden children

- **WHEN** the scope cannot see any ordered child of an entity
- **THEN** the entity carries no `engagement`

#### Scenario: No Profile

- **WHEN** the request has no acting Profile
- **THEN** no item carries `engagement`

### Requirement: Cards have an accessible name with title, type and state

Every media and entity card SHALL expose an accessible name containing its title, its type where the
item states one, and its playback state where the Profile has one.

#### Scenario: Partly played item

- **WHEN** a card for a partly played item is rendered
- **THEN** its accessible name contains the title and the time left

#### Scenario: Missing name

- **WHEN** a card renders without an accessible name
- **THEN** the automated accessibility test fails

### Requirement: Cards show playback state on the artwork

A card SHALL draw a progress bar for a partly played item, a finished mark for a completed item and
the remaining count for an ordered container with children left, on every shelf and grid.

#### Scenario: Shelf and grid

- **WHEN** a partly played item appears on a Home shelf, a library grid or search results
- **THEN** its card shows a progress bar sized to the played fraction

#### Scenario: Container with children left

- **WHEN** an ordered container has children the Profile has not finished
- **THEN** its card shows the remaining count

### Requirement: Cards offer Play or Resume and a More menu

A card SHALL reveal a Play control (Resume when partly played) and a More menu on hover and on
keyboard focus, and SHALL keep both reachable on touch devices without hover.

#### Scenario: Keyboard

- **WHEN** a keyboard user tabs to a card's More control and presses Enter
- **THEN** the menu opens, arrow keys move between entries and Escape closes it, returning focus

#### Scenario: Touch

- **WHEN** the device has no hover
- **THEN** the More control is visible without hover and opens the menu on tap

#### Scenario: Reduced motion

- **WHEN** the user prefers reduced motion
- **THEN** the controls appear without transition

#### Scenario: Mark played from the menu

- **WHEN** the user chooses the played-state entry in a card's More menu
- **THEN** the item's played state for the acting Profile is toggled and the card's state follows
  without a reload

### Requirement: Card state refreshes without a reload

Card playback state SHALL reflect playback that happened in the session without a page reload.

#### Scenario: Return from the player

- **WHEN** the user plays part of an item and returns to a shelf or grid
- **THEN** the item's card shows the new progress
