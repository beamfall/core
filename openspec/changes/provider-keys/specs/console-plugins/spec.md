## ADDED Requirements

### Requirement: Provider keys in plugin cards

Console > Plugins SHALL show each plugin's scraper credentials, grouped by AuthRef, inside that
plugin's card under a human label derived from the plugin's scraper display names, with a status
chip and save, validate and disable controls. The raw AuthRef MAY appear only as secondary detail.

#### Scenario: Missing key is visible without scrolling

- **WHEN** a plugin has a required credential whose status is missing
- **THEN** its card header shows a "key needed" chip and the key field is shown without expanding

#### Scenario: Label from plugin data

- **WHEN** two scrapers sharing an AuthRef are named "TMDB (Movies)" and "TMDB (TV)"
- **THEN** the provider key is labelled "TMDB"

### Requirement: Compact plugin list

Console > Plugins SHALL render one card per plugin with its configuration, permissions, status,
actions and lifecycle controls collapsed behind a details toggle, and SHALL NOT render separate
page-level scraper or credential tables.

#### Scenario: Page opens

- **WHEN** an admin opens Console > Plugins
- **THEN** each plugin shows its name, activation switch and state, and its details stay collapsed
