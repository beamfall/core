# plugin-inventory Specification

## Purpose
Keep Core starting when its stored plugin inventory holds a plugin built for a plugin SDK it no longer embeds, while a corrupt or tampered inventory still stops startup.

## Requirements

### Requirement: A stored plugin outside the embedded SDK range does not stop startup

Core SHALL start when its plugin inventory holds an intact entry whose declared SDK range excludes
the plugin SDK Core embeds. Core SHALL keep that entry in the inventory, SHALL NOT load it, and
SHALL log a warning naming the plugin, its SDK range and the embedded SDK. Core SHALL still apply a
staged bundle set at startup when one is present and valid.

#### Scenario: Last-good plugin left behind by a plugin-sdk bump

- **WHEN** last-good holds a plugin whose SDK ceiling is older than the embedded SDK and nothing is
  staged
- **THEN** Core starts with every other plugin loaded, the incompatible plugin is not loaded, and
  its entry is still in last-good

#### Scenario: Compatible replacement staged for the incompatible plugin

- **WHEN** last-good holds an incompatible plugin and a compatible version of it is staged
- **THEN** Core applies the staged version at startup and loads it

### Requirement: Corrupt or tampered inventory still fails closed

Core SHALL refuse to start when the last-good plugin inventory cannot be decoded, when an entry's
stored artifact digest does not match its artifact, or when an entry's origin, trust state,
provenance or bundle declaration is invalid. SDK range incompatibility alone SHALL NOT be treated
as one of these failures.

#### Scenario: Tampered last-good entry

- **WHEN** a last-good entry's artifact digest does not match its artifact
- **THEN** Core refuses to start and reports the plugin inventory as invalid

### Requirement: Staging never admits an incompatible artifact

Staging SHALL reject a new artifact whose SDK range excludes the embedded SDK. Staging a different
plugin SHALL keep an existing incompatible entry, and staging a new version of an incompatible
plugin SHALL replace that entry by plugin ID.

#### Scenario: Staging a replacement

- **WHEN** an owner stages a compatible version of a plugin whose stored entry is incompatible
- **THEN** the staged inventory holds exactly one entry for that plugin, the new version
