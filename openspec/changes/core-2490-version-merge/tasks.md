# Tasks (CORE-2490)

- [x] 1. Store: `MergeSplitVersions` moves each split copy's file onto the survivor under a derived label, copies user state without overwriting, trashes the copy through a `merge-version` batch, and skips encrypted libraries, disabled trash and undone merges; Go tests
- [x] 2. Store: `UndoBatch` restores a `merge-version` split; Go tests
- [x] 3. Scan: merge split copies of one title when a directory's cataloged files are read; Go test for a rescan of a pre-existing split
- [x] 4. Store: a merge leaves out Extras and Media in any Media relation, Trash Restore refuses a merged copy until its batch is undone (the GraphQL restore passes that message through, coded CONFLICT), and an undo from a scope that cannot see a merged copy is refused rather than skipping it; Go tests
