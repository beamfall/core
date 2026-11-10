## ADDED Requirements

### Requirement: Floating transport card

The web player transport SHALL render as one floating card inset from the stage edges, with no scrim
gradient, and SHALL use an opaque surface when the user prefers reduced transparency.

#### Scenario: Default presentation

- **WHEN** the player chrome is visible
- **THEN** the transport is a rounded card with a hairline border inset from the stage edges

### Requirement: Icon controls keep accessible names

Every icon-only transport control SHALL expose a non-empty accessible name and a visually hidden text
label.

#### Scenario: Icon button name

- **WHEN** the transport renders
- **THEN** each icon-only button has a non-empty accessible name

### Requirement: Touch target floor

Every transport button SHALL have a hit area of at least 44 by 44 CSS pixels.

#### Scenario: Mobile viewport

- **WHEN** the player is shown at 390 by 844
- **THEN** each transport button measures at least 44 by 44
