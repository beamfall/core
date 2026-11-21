## ADDED Requirements

### Requirement: Home leads with resumable items

When the Home layout includes a Continue Watching shelf, the Viewer SHALL show
the Recently Added hero only after that shelf settles empty or failed. While
the shelf loads, the Viewer SHALL show neither the hero nor its placeholder.
When the shelf has items, it SHALL be the first Home block.

#### Scenario: Returning Profile opens Home

- **WHEN** the Continue Watching shelf loads with items
- **THEN** Home shows that shelf first and no Recently Added hero

#### Scenario: New Profile opens Home

- **WHEN** the Continue Watching shelf loads empty
- **THEN** Home shows the Recently Added hero above its shelves

#### Scenario: Continue Watching is still loading

- **WHEN** Recently Added artwork is ready before the Continue Watching shelf settles
- **THEN** Home shows no hero and no hero placeholder until the shelf settles
