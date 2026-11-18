# Tasks (CORE-2490)

- [ ] 1. Store: `MergeSplitVersions` moves each split copy's file onto the survivor under a derived label, copies user state without overwriting, trashes the copy through a `merge-version` batch, and skips encrypted libraries, disabled trash and undone merges; Go tests
- [ ] 2. Store: `UndoBatch` restores a `merge-version` split; Go tests
- [ ] 3. Scan: merge split copies of one title when a directory's cataloged files are read; Go test for a rescan of a pre-existing split
