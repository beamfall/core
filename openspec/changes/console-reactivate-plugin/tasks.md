## 1. Fix

- [ ] 1.1 Build a registry of every shipped bundle in `internal/app` and pass it to the graph handler.
- [ ] 1.2 Read that registry in `consoleConfig`, `setPluginActive`, `setPluginConfig` and `uninstallPluginData`.
- [ ] 1.3 Graph and app tests for the listing, the reactivation and the unshipped-id refusal.

## 2. Land

- [ ] 2.1 `make gate` green on the landing head.
- [ ] 2.2 `openspec archive console-reactivate-plugin` after the change lands.
