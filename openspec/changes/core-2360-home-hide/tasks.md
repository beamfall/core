# Tasks (CORE-2360)

- [x] 1. Catalog migration `0121_library_show_on_home.sql` (additive column, default shown)
- [x] 2. Store: `Library.ShowOnHome`, `SetLibraryShowOnHome`, `SourceQuery.Home` narrowing of
      aggregate source queries; store tests
- [x] 3. Graph: `LibraryInfo.showOnHome`, admin `setLibraryShowOnHome` (audited), `sourceItems(home:)`;
      graph tests
- [x] 4. Regenerate wire goldens and the management manifest; publish the wire contract minor;
      regenerate the backup fixture corpus
- [x] 5. Web: Console > Libraries "Show on Home" switch; Home and See all from Home pass `home: true`;
      strings in every shipped locale; vitest
- [ ] 6. Native clients send `home: true` for Home shelves (consumer follow-up)
- [ ] 7. `make gate` at landing; `openspec archive core-2360-home-hide`
