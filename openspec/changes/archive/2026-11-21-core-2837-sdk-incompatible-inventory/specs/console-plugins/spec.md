## ADDED Requirements

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
