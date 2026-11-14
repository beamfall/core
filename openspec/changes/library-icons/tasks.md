# Tasks (CORE-1200)

- [x] 1. Icon vocabulary and media-class default in core (`internal/store/library_icon.go`)
- [x] 2. Owner's choice in `server_config` (`library.icon.<libraryID>`, no migration); `Library.Icon`,
      `SetLibraryIcon`, store tests
- [x] 3. Manifest `LibrarySummary.icon` (owner's choice, else default); manifest tests
- [x] 4. Icon write on `setConsoleConfig` (`libraryID` + `libraryIcon`; admin, validated, audited);
      `icon`, `iconChoice`, `defaultIcon` on `LibraryInfo`; graph tests
- [x] 5. Regenerate wire goldens, manifest goldens and the unpublished 2.0.0 wire-contract
      candidate (no publication, no new admin operation); update `docs/`
- [x] 6. Web sidebar draws the icon, monogram fallback; tests
- [x] 7. Console icon picker (native radio group, optimistic save settled into one write,
      rollback); in-session manifest re-read so the sidebar follows; strings in en, fr, de, es and
      pt; tests
- [ ] 8. Owner review in the running UI; `make gate` at landing; `openspec archive library-icons`
