## 1. Server

- [x] 1.1 `store.InspectBackupManifest`: contents, versions and the refusal from the existing schema guard
- [x] 1.2 `internal/restorestage`: inspect, stage (marker, identity preflight), cancel, status, claim, result
- [x] 1.3 `serve` applies a staged restore before locking and opening the store, through the CLI restore path
- [x] 1.4 Admin-Session fields `stagedRestore`, `inspectBackup`, `stageRestore`, `cancelStagedRestore`
- [x] 1.5 Go tests: stage, apply on start with a rollback copy, failure rollback, version and identity refusal

## 2. Console

- [x] 2.1 Backup page restore panel: key field, contents, replace-identity choice, stage, cancel, last result
- [x] 2.2 Strings in `strings/staged-restore.ts`, registered in the catalog and the registry baselines
- [x] 2.3 Vitest for the console flow and the GraphQL requests

## 3. Contract

- [x] 3.1 Wire goldens, field inventory and management manifests regenerated
