- [x] Prune folders that sort wholly before the checkpoint on a resumed walk (`subtreeBeforeCheckpoint`).
- [x] Keep the empty-root guard honest when a resumed walk pruned folders.
- [x] Stat only media-named regular files in the pre-count walk.
- [x] Run a contextual folder's preflight probes within the worker budget (`probeSiblings`).
- [x] Tests: `TestSubtreeBeforeCheckpoint`, `TestScanResumeDoesNotReadFoldersBeforeCheckpoint`,
      `TestScanResumePastPrunedFoldersStillMarksMissing`,
      `TestContextualPreflightProbesWithinWorkerBudget`.
