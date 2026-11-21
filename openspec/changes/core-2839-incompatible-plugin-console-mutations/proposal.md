# Let operators configure and uninstall SDK-incompatible plugins

Ticket: CORE-2839 (`queue:core:main`). Follow-up to CORE-2837.

## Why

CORE-2837 keeps a stored plugin whose SDK range excludes the embedded plugin SDK, lists it in
Console > Plugins as inactive, and refuses to activate it. `uninstallPluginData` and
`setPluginConfig` still resolve plugins through the shipped registry only, so for that plugin they
answer "unknown plugin". An operator can see the plugin but cannot purge the data it left behind,
or edit or clear its stored config (such as a credential), until a compatible version is staged.

## What Changes

- `uninstallPluginData` accepts an incompatible plugin. It purges the Entity data of the Kinds that
  plugin declares from the Libraries bound to it, and records the plugin as deactivated, the same as
  for a shipped plugin, so a compatible version staged later does not start until it is reactivated.
- `setPluginConfig` accepts an incompatible plugin and validates values against the config fields it
  declares. The stored config stays with the plugin ID for a compatible version.
- Unchanged: activating an incompatible plugin is still refused with the SDK reason, and an unknown
  or suppressed plugin ID is still "unknown plugin".

## Impact

- `internal/graph` (Console plugin mutations) only. No wire schema change, no migration, no new
  Store method.
