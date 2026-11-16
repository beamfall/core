## 1. Store (beamfall)

- [x] 1.1 `SetEncryptedLibraryDerivativePurgers` seam, read with the key eraser under the purge lock
- [x] 1.2 `completeLibraryPurge` runs each purger with no lock held; a failure keeps the pending row
- [x] 1.3 Tests: purge calls every purger; a failing purger is retried by `CompletePendingLibraryPurges`

## 2. Wiring

- [x] 2.1 The app installs the rewrap purgers (subtitle cache, precomputed renditions) before finishing pending purges
