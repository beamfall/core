# Tasks (CORE-2473)

- [x] 1. Store: derive labels for two unlabelled Versions (resolution, then container) in the attach transaction, moving the default file to its label; slot conflicts reported as `ErrVersionSlotTaken`; Go tests
- [x] 2. Scan: ask for derived labels when neither file has a parsed label; Go tests for a fresh scan, an unprobed pair, a copy added beside a cataloged film, and label stability on rescan
- [x] 3. Follow-up ticket for merging splits that earlier scans made (CORE-2490)
