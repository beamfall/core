# web-player-visual-parity Specification

## Purpose
Keep every web player transport control accessible: icon-only controls carry an accessible name and every button meets the touch-target floor. The transport's layout (controls on a bottom gradient scrim) is specified in `web-player-overlay`, which replaced the floating card this capability first described (CORE-1141).

## Requirements

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
