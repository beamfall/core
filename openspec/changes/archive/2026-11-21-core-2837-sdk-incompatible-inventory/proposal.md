# Keep starting when a stored plugin no longer fits the embedded plugin SDK

Ticket: CORE-2837 (`queue:core:main`).

## Why

Core keeps installed declarative plugins in `plugins/inventory/last-good.json` and promotes a
staged replacement from `staged.json` at startup. `plugin.OpenInventory` validates last-good and
fails when any entry's SDK range excludes the plugin SDK Core embeds, and `app.New` returns on that
error before `Inventory.Apply` can run. A Core release that bumps plugin-sdk past a stored plugin's
ceiling therefore refuses to start, and a compatible replacement staged for that plugin can never be
applied. Found by Codex review of CORE-2831; the 0.4.4 and 0.4.5 bumps had the same exposure. No
deployed server has an inventory directory today, so nothing is affected yet; it matters once
owners install third-party plugins.

## What Changes

- An inventory entry whose artifact is intact (digest, origin, trust and provenance all check out)
  but whose SDK range excludes the embedded SDK is **incompatible**, not invalid. Startup keeps it
  on disk, leaves it out of the loaded plugin set, and logs a warning naming the plugin, its range
  and the embedded SDK.
- `Inventory.Apply` still promotes a staged bundle set. A compatible staged replacement for an
  incompatible plugin is applied and loads.
- Staging still rejects a new artifact whose SDK range excludes the embedded SDK. Staging another
  plugin keeps an incompatible entry in place, and staging a replacement swaps it by plugin ID.
- Console > Plugins lists an incompatible plugin as inactive, with an `sdk-compatibility` status row
  in state `incompatible` that names its range and the embedded SDK. Activating it is refused with
  a client error that says why.
- Unchanged: a corrupt, tampered or otherwise invalid inventory (bad JSON, digest mismatch, unknown
  origin or trust state, duplicate IDs, kind collisions) still fails closed.

## Impact

- `internal/plugin` (inventory), `internal/app` (startup wiring), `internal/graph` (Console plugin
  listing and `setPluginActive`).
- No wire schema change: the incompatible plugin uses the existing `consoleConfig.plugins` shape and
  a status row. No migration, no new Store method.
