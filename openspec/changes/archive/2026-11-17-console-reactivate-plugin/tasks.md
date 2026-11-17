## 1. Fix

- [x] 1.1 Build a registry of every shipped bundle in `internal/app` and pass it to the graph handler.
- [x] 1.2 Read that registry in `consoleConfig`, `setPluginActive`, `setPluginConfig` and `uninstallPluginData`.
- [x] 1.3 Graph and app tests for the listing, the reactivation and the unshipped-id refusal.

## 2. Land

- [x] 2.1 `make gate` green on the landing head.
- [x] 2.2 `openspec archive console-reactivate-plugin` after the change lands.
