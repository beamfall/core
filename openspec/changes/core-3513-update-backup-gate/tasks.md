# Tasks

- [x] 1. Record ADR-0243 amending ADR-0079 and ADR-0192 §6, and re-record `console-ops-pack` / `console-update-warning` mockup approvals.
- [x] 2. Extend `internal/backup/status.go` (`LastBinaryVersion`, `LastUpgradeBypass`, `LastUnverifiedUpgrade`, `EvaluateBootUpgrade`, `RecordUpgradeBypass`, `unverified-upgrade` banner ack) and tests.
- [x] 3. Add `auth.Service.VerifyAdminReauth` and tests in `internal/auth`.
- [x] 4. Add signed dist verification, update staging (`internal/app/update_stage.go` + `internal/release`), boot-time detection before `store.Open`, webhook outbox events (`internal/store/backup_events.go`), `--allow-unverified-upgrade`, and `beamfall upgrade stage|apply|status` CLI commands.
- [x] 5. Wire GraphQL fields (`authorizeBackupUpgradeBypass`, `stageCoreUpdate`, `cancelStagedCoreUpdate`, `stagedCoreUpdate`), update wire goldens + conformance (`internal/graph`, `internal/conformance`), and add end-to-end tests.
- [x] 6. Update Console UI (`Operations.tsx`, `BackupOpsSection.tsx`, `lib/graphql/`, `strings/backup-ops.ts`) and run focused Go, Vitest, OpenSpec, and Codex review checks before pushing `wave/core-3513`.
