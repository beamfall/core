## ADDED Requirements

### Requirement: Viewer and console pages start with a skip link

Every viewer and console page SHALL have, as its first Tab stop, a link that is visible while it
has focus and moves focus to the page's main content when activated.

#### Scenario: Keyboard user on a browse page

- **WHEN** a keyboard user presses Tab once on a viewer browse page
- **THEN** a visible "Skip to main content" link has focus

#### Scenario: Skipping the header

- **WHEN** the user activates the skip link
- **THEN** focus moves to the main content and the next Tab reaches its first control
