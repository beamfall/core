## 1. Store (beamfall)

- [x] 1.1 Purge transaction writes a `library.purge-pending.<id>` row for an encrypted Library
- [x] 1.2 After the commit: close the plane, wipe the working copy, remove ciphertext and backups, erase keys, clear the row
- [x] 1.3 `CompletePendingLibraryPurges` finishes owed teardowns; the retention sweep calls it
- [x] 1.4 Refuse an encrypted purge only when no key eraser is installed or another Library shares its folder
- [x] 1.5 Tests: full purge, refusal leaves it restorable, failure partway finished by the sweep, crash finished at start

## 2. Key material and wiring

- [x] 2.1 `libencrypt.Manager.Erase` drops the DEK and clears the owner-bound secrets, idempotently
- [x] 2.2 The app installs the eraser and finishes pending purges at start
