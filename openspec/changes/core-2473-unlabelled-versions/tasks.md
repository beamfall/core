# Tasks (CORE-2473)

- [ ] 1. Store: derive labels for two unlabelled Versions (resolution, container, file name) in the attach transaction, moving the default file to its label; slot conflicts reported as `ErrVersionSlotTaken`; Go tests
- [ ] 2. Scan: ask for derived labels when neither file has a parsed label; Go tests for a fresh scan, an unprobed pair, a copy added beside a cataloged film, and label stability on rescan
- [ ] 3. Follow-up ticket for merging splits that earlier scans made
