# console-plugins Specification

## Purpose
Console > Plugins shows each plugin as a compact card and lets the owner enter a metadata provider's API key on that plugin's card, so scrapers can fetch details and artwork without a separate credential table keyed by raw AuthRef.

## Requirements

### Requirement: Provider keys in plugin cards

Console > Plugins SHALL show each plugin's scraper credentials, grouped by AuthRef, inside that
plugin's card under a human label derived from the plugin's scraper display names, with a status
chip and save, validate and disable controls. The raw AuthRef MAY appear only as secondary detail.
A credential the server reports as not required SHALL show "Optional" while unset, and a missing
required key SHALL be called out only when a Library uses the plugin.

#### Scenario: Missing key is visible without scrolling

- **WHEN** a plugin that a Library uses has a required credential whose status is missing
- **THEN** its card header shows a "key needed" chip and the key field is shown without expanding

#### Scenario: Label from plugin data

- **WHEN** two scrapers sharing an AuthRef are named "TMDB (Movies)" and "TMDB (TV)"
- **THEN** the provider key is labelled "TMDB"

#### Scenario: Optional key

- **WHEN** a scraper's credential is unset and the server reports it not required, because the
  plugin's embedded default key covers the same capability
- **THEN** its chip reads "Optional" and no "key needed" chip is shown

#### Scenario: Plugin without a Library

- **WHEN** no Library uses a plugin whose required key is missing
- **THEN** its card shows no "key needed" chip

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

### Requirement: Incompatible plugins are listed, not hidden

Console > Plugins SHALL list a stored plugin that Core did not load because its SDK range excludes
the embedded plugin SDK. The plugin SHALL be shown as inactive with a status row that says it is
incompatible and names its SDK range and the embedded SDK. A request to activate it SHALL be
refused with a client error that names the incompatibility.

#### Scenario: Plugin left behind by a Core upgrade

- **WHEN** an admin opens Console > Plugins after an upgrade left a stored plugin incompatible
- **THEN** the plugin is listed with its activation switch off and an `incompatible` SDK
  compatibility status naming its range

#### Scenario: Activating an incompatible plugin

- **WHEN** an admin tries to activate an incompatible plugin
- **THEN** the request is refused with an error that says the plugin does not support this
  server's plugin SDK
