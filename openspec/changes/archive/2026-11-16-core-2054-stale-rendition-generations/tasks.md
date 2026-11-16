## 1. Store (beamfall)

- [x] 1.1 Record the generations this process starts; drop them on publish or fail, including a Sealed fail
- [x] 1.2 `FailStalePlaybackRenditionGenerations` fails unowned `generating` rows on every reachable plane
- [x] 1.3 Unseal fails the Library's unowned `generating` rows before the derivative reconcile
- [x] 1.4 Tests: crash residue failed, restart sweep, Seal-aborted run failed on Unseal, live run kept

## 2. Startup

- [x] 2.1 `SweepPrecomputeResidue` runs the stale sweep before job workers resume
- [x] 2.2 Test: startup sweep fails a crash-left generating row
