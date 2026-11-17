Ticket: CORE-2235 (`queue:core:main`). Status: proposed.

## Why

Turning a plugin off in Console > Plugins is meant to be reversible: deactivation leaves the
plugin's data inert and the switch should bring it back. Today the server drops a deactivated
bundle before it builds the plugin registry, and the Console reads that same registry. After the
next restart the plugin is gone from the Console and `setPluginActive` answers "unknown plugin",
so the only way back is the offline `apply-config` command.

## What Changes

- The Console keeps the full set of plugins this server ships, including deactivated ones.
  `consoleConfig` lists them with `active: false`, and `setPluginActive`, `setPluginConfig` and
  `uninstallPluginData` accept them.
- The live registry is unchanged: a deactivated plugin's Entity Kinds, Sources and scrapers still
  do not load, and it stays out of the `plugins` query and the library type picker.

## Non-goals

- Applying an activation change without a restart.
- Changing which plugins are suppressed on managed deployments.

## Failure modes

- A deactivated plugin that was reactivated still needs a restart before it loads; the Console
  already says so for deactivation.
- A plugin id that this server does not ship stays a client error, so the override table cannot
  carry a phantom plugin.

## Acceptance evidence

- A graph test starts a handler whose live registry lacks a deactivated plugin and proves
  `consoleConfig` lists it inactive, `setPluginActive(active: true)` succeeds and an unshipped id
  is still refused.
- An app test proves a server opened with a deactivated plugin keeps it in the registry the
  Console reads and leaves it out of the live registry.
- `make gate` green on the landing head.

## Rollback

Revert the commit. No stored data changes shape.
