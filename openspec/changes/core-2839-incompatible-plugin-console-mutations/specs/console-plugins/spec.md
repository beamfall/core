## ADDED Requirements

### Requirement: Incompatible plugins stay configurable and uninstallable

Console > Plugins SHALL let an admin edit the stored config of a plugin that Core did not load
because its SDK range excludes the embedded plugin SDK, validated against the config fields that
plugin declares. Uninstalling such a plugin SHALL purge the Entity data of its declared Kinds from
the Libraries bound to it and SHALL record the plugin as deactivated, as for a loaded plugin.

#### Scenario: Uninstalling an incompatible plugin

- **WHEN** an admin confirms uninstall for an incompatible plugin that has Libraries bound to it
- **THEN** that plugin's Entity data is purged, the plugin is recorded as deactivated, and it is
  still listed as incompatible

#### Scenario: Editing an incompatible plugin's config

- **WHEN** an admin saves or clears a declared config field of an incompatible plugin
- **THEN** the change is stored and the plugin stays inactive
