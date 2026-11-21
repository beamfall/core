- [ ] `graph`: resolve a Console-managed plugin from the shipped registry or the incompatible list,
      excluding suppressed plugins, and use it in `uninstallPluginData` and `setPluginConfig`
- [ ] Tests: uninstall purges an incompatible plugin's data and deactivates it; config saves and
      clears for an incompatible plugin; activation is still refused
