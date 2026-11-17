## ADDED Requirements

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
