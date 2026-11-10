## ADDED Requirements

### Requirement: Select triggers have an accessible name

Every Select trigger in the Manage area SHALL expose a localized accessible name.

#### Scenario: Operations form

- **WHEN** the Manage operations page is audited with axe
- **THEN** there is no `button-name` violation

### Requirement: One h1 per Manage and Viewer route

Each Manage and Viewer route SHALL render exactly one `h1` naming the page, and section or card titles
SHALL be level-2 headings or lower.

#### Scenario: Manage route

- **WHEN** a Manage route is rendered
- **THEN** exactly one `h1` is present and it names the route

#### Scenario: Viewer route without a loaded title

- **WHEN** the player or browse page has no title yet
- **THEN** the single `h1` shows the brand name
