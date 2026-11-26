# Enforce CORE-0778 Update Backup Gate (CORE-3513)

Ticket: `CORE-3513`
ADRs: `ADR-0243` (amending `ADR-0079`, `ADR-0192` §6, referencing `ADR-0085`)

## Problem

`CORE-0778` landed ADR-0192's backup status tracking and Console update-gate display as advisory only:

1. Core has no signed update staging or offline CLI install path (`ADR-0079` prohibited updates cleanly, leaving no install action for the gate to allow or block).
2. Elevated bypass of a blocked `soft` gate lacks step-up Admin password re-authentication (`verifyAdminReauth`), typed confirmation naming target version and failed Backup Set ID, durable audit record, and webhook emission.
3. Server boot does not detect binary version upgrades prior to `store.Open` SQLite schema migrations, so out-of-band binary replacements bypass both `hard` blocking and `soft` warning/banner/webhook reporting.
4. Console `<BackupUpdateGate />` renders only a static 4-item checklist without gate policy controls, step-up bypass authorization, signed dist staging, or unverified-upgrade banner acknowledgement.

## Proposal

1. Extend `internal/backup.Status` with `LastBinaryVersion`, `LastUpgradeBypass`, `LastUnverifiedUpgrade`, and `UnverifiedUpgradeBannerAckAt`, plus pure evaluation and transition methods (`RecordUpgradeBypass`, `AcknowledgeBanner` for `"unverified-upgrade"`, `EvaluateBootUpgrade`).
2. Add `VerifyAdminReauth` to `internal/auth.Service` using the rate-limited admin auth backoff lane and existing `verifyAdminReauth` verifier.
3. Add a signed local update staging & offline CLI install path in `internal/release` + `internal/app` + `cmd/beamfall` (`stageCoreUpdate` / `cancelStagedCoreUpdate` / `stagedCoreUpdate` in Console GraphQL, plus `beamfall upgrade stage|apply|status` and `--allow-unverified-upgrade` boot flag), gated on `UpdateGateClear()` (or a matching `soft` elevated bypass).
4. Enforce boot-time binary version transition detection inside `app.New` prior to `store.Open`, emitting structured warnings and `backup.upgrade_bypassed` / `backup.upgrade_without_backup` webhook outbox events once `store` opens.
5. Upgrade Console `<BackupUpdateGate />` and `BackupOpsSection` in `internal/web/app` to match `update-backup-gate.html` across all 5 locales.
