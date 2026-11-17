# console-plugins Specification

## Purpose
Console > Plugins shows each plugin as a compact card and lets the owner enter a metadata provider's API key on that plugin's card, so scrapers can fetch details and artwork without a separate credential table keyed by raw AuthRef.

## Requirements

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

### Requirement: Deactivated plugins stay manageable

Console > Plugins SHALL list every plugin this server ships, including a plugin that is
deactivated and therefore not loaded, and SHALL let an admin activate it again. A deactivated
plugin SHALL stay out of the live plugin registry until the server restarts with it active.

#### Scenario: Deactivated plugin after a restart

- **WHEN** an admin deactivates a plugin and the server restarts
- **THEN** Console > Plugins still lists the plugin, with its activation switch off

#### Scenario: Reactivating

- **WHEN** an admin turns a deactivated plugin's activation switch on
- **THEN** the change is saved and the plugin is listed as active

#### Scenario: Deactivated plugin does not load

- **WHEN** the server starts with a plugin deactivated
- **THEN** that plugin is absent from the library type picker and none of its Entity Kinds, Sources or scrapers load

#### Scenario: Unshipped plugin id

- **WHEN** a request names a plugin id this server does not ship
- **THEN** the request is refused as an unknown plugin
