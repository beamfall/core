# web-a11y-names-headings Specification

## Purpose
Give every select trigger an accessible name and every Manage, Console and Viewer route exactly one h1, so assistive technology can name the controls and navigate the pages.

## Requirements

### Requirement: Select triggers have an accessible name

Every Select trigger in the Manage and Console areas SHALL expose a localized accessible name.

#### Scenario: Console access, schedules, remote access and plugins

- **WHEN** any Console route is audited with axe
- **THEN** there is no `button-name` violation

#### Scenario: Operations form

- **WHEN** the Manage operations page is audited with axe
- **THEN** there is no `button-name` violation

### Requirement: One h1 per Manage, Console and Viewer route

Each Manage, Console and Viewer route SHALL render exactly one `h1` naming the page, and section or card titles
SHALL be level-2 headings or lower.

#### Scenario: Manage route

- **WHEN** a Manage route is rendered
- **THEN** exactly one `h1` is present and it names the route

#### Scenario: Console route

- **WHEN** a Console route is rendered, signed in or admin-locked
- **THEN** exactly one `h1` is present and it names the route; section titles are level-2 headings

#### Scenario: Viewer route without a loaded title

- **WHEN** the player or browse page has no title yet
- **THEN** the single `h1` shows the brand name
