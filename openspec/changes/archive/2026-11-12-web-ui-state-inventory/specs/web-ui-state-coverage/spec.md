## ADDED Requirements

### Requirement: The web app has one inventory of its UI states

The repository SHALL keep one inventory of web UI states in `internal/web/app/e2e/lib/ui-states.mjs`.
Each row SHALL name a screen and either an in-page state of that screen or one context the screen
is shown in (a role, a breakpoint, a locale or an accessibility variant). Row ids SHALL be unique.
Every leaf route of the app, manage, console and viewer routers SHALL appear as a screen.

#### Scenario: A new route is added without an inventory row

- **WHEN** a leaf route or a Console section is added and the inventory has no screen for it
- **THEN** the inventory unit test fails and names the route

#### Scenario: Two rows share an id

- **WHEN** the inventory defines the same row id twice
- **THEN** the inventory unit test fails and names the id

### Requirement: Every UI state is covered or listed as a known gap

The unfiltered `make web-ui-e2e-smoke` run SHALL treat a row as covered when a passed test
declared it, or when the visual-parity attachment the row names passed. The run SHALL fail when a
row is neither covered nor in the reviewed allowlist, when an allowlisted row is covered, or when a
test declares an id the inventory does not define. It SHALL write the join of rows to covering
tests as `ui-state-coverage.json` in the Playwright output directory.

#### Scenario: A state loses its only test

- **WHEN** the only test that covered a row stops declaring it and the row is not allowlisted
- **THEN** the smoke run fails and names the row

#### Scenario: A known gap gets a test

- **WHEN** a test starts covering a row that is still in the allowlist
- **THEN** the smoke run fails until the row is removed from the allowlist

#### Scenario: A test declares a misspelt row

- **WHEN** a test declares an id the inventory does not define
- **THEN** the smoke run fails and names the id and the test
