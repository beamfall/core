## 1. Document and apply

- [x] 1.1 Add the `libraries` field and validation to the config document
- [x] 1.2 Match by title plus plugin, create missing libraries, union roots, never remove
- [x] 1.3 Sort libraries and roots on export for a stable round trip

## 2. Offline mode

- [x] 2.1 Add `--data-dir` to `export-config` and `apply-config`, refusing a live server lock
- [x] 2.2 Add `CreateUngrantedLibraryWithRoots` for pre-first-run creation; grant to the owner otherwise

## 3. Tests and docs

- [x] 3.1 Tests: create, idempotent re-apply, root addition, invalid plugin and roots, round trip
- [x] 3.2 Update CLI help, README, architecture and the deploy runbook
