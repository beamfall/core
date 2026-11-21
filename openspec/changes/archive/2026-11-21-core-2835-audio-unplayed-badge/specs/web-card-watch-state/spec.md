## MODIFIED Requirements

### Requirement: Cards show playback state on the artwork

A card SHALL draw a progress bar for a partly played item, a finished mark for a completed item and
the remaining count for an ordered container with children left, on every shelf and grid. An audio
container SHALL NOT show the remaining count. The remaining count SHALL use the same quiet
translucent pill as the finished mark, not an accent fill.

#### Scenario: Shelf and grid

- **WHEN** a partly played item appears on a Home shelf, a library grid or search results
- **THEN** its card shows a progress bar sized to the played fraction

#### Scenario: Container with children left

- **WHEN** an ordered video container has children the Profile has not finished
- **THEN** its card shows the remaining count in a translucent pill

#### Scenario: Audio container with children left

- **WHEN** an audio container has children the Profile has not finished
- **THEN** its card shows no remaining count
