- [x] `plugin.Inventory`: classify intact SDK-out-of-range entries as incompatible; keep them on
      disk, out of the loaded set, and replaceable by plugin ID; keep fail-closed integrity checks
- [x] `app.New`: log each incompatible plugin and pass the list to the graph handler
- [x] `consoleConfig.plugins`: list incompatible plugins inactive with an `sdk-compatibility`
      status row; `setPluginActive` refuses them with a clear client error
- [x] Tests: plugin and app startup with an incompatible last-good entry, with and without a
      compatible staged replacement; tampered last-good still fails; graph listing and refusal
