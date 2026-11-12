# Tasks (CORE-1201)

- [x] 1. Store: list deleted libraries, `RestoreLibrary`, `PurgeLibrary` (one transaction, Custodial
  refused, files untouched), retention sweep variant; `library.restore` and `library.purge` activity
- [x] 2. Watcher can forget a library; scheduler can unregister a schedule
- [x] 3. Graph: `trashedLibraries`, `restoreLibrary`, `purgeLibrary` resolvers; `deleteLibrary`,
  restore and purge notify the library observer
- [x] 4. App: observer stops watcher and schedule at delete, resumes and rescans at restore, removes
  generated artwork at purge; retention sweep purges libraries past `TrashRetention`
- [x] 5. Go tests: store, graph, watcher, schedule, and the delete-stops / restore-resumes proof
- [ ] 6. Dispatcher registration, admin-field guard and nested schema entries; conformance and
  wire-contract goldens regenerated with `make regen-wire-goldens`
- [x] 7. Web: named `TrashedLibraries`, `RestoreLibrary`, `PurgeLibrary` operations
- [x] 8. Web: "Recently deleted" group, Restore, confirmed "Delete permanently", Undo after delete,
  delete dialog copy names the restore path
- [x] 9. Strings in en, fr, de, es and pt; `strings.test.ts` baselines
- [x] 10. Focused vitest: list, restore, purge confirm and cancel, undo, errors
- [ ] 11. Owner review in the running UI; `make gate` at landing; `openspec archive library-trash-restore`
