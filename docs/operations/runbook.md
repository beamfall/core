---
name: operations-runbook
description: Operator runbook index for the Beamfall Core server (deploy, observe, back up, recover).
updated: 2026-06-21
---

# Beamfall Core — operations runbook

Operator-facing procedures for running the AGPL Core server. Pre-launch numeric targets are from
**DEC-6**: RPO <= 24h, RTO <= 1h, retention 7 daily + 4 weekly, alerts on relay over-cap,
transcode-failure, and HTTP 5xx > 1%, and signing-key rotation every 12 months or on compromise.
Security/incident procedures live under
[`../security/runbooks/`](../security/runbooks/).

## What exists today (primitives these runbooks build on)

- **Backup:** `store.Store.Backup` / `BackupWithOptions` produce a consistent SQLite snapshot via
  `VACUUM INTO` plus a backup manifest (`internal/store/backup.go`).
- **Restore:** `store.RestoreBackup(ctx, manifestPath, dataDir)` (`internal/store/backup.go`).
- **Operator responsibility:** `docs/operations/operator-responsibility.md`.
- **Metrics:** `GET /metrics` (Prometheus text v0.0.4) exposes `beamfall_up`,
  `beamfall_jobs{class,status}`, `beamfall_sessions_active`, `beamfall_transcode_active`,
  `beamfall_library_media_total` (+ `*_error` sentinels). Every scrape needs the bearer in
  `<data-dir>/secrets/metrics-scrape-token` (0600, rotated every start) — **including from
  loopback**, so an uncredentialed local scrape returns 403 after upgrade — unless the
  `--public-metrics` / `BEAMFALL_PUBLIC_METRICS=true` opt-in is set. Reading the token as a
  different user needs setup: see
  [`../security/runbooks/metrics.md`](../security/runbooks/metrics.md).
- **Trash / recovery:** destruction is recoverable by default (invariant 6).

## Procedure index

| Runbook | Purpose |
|---|---|
| [deploy](../security/runbooks/deploy.md) | Stand up / update a server instance |
| [logs](../security/runbooks/logs.md) | Where logs go and how to read them |
| [metrics](../security/runbooks/metrics.md) | Scrape, dashboards, and alert wiring |
| [backup-restore](../security/runbooks/backup-restore.md) | Snapshot and recover the database |
| [upgrade-test](../security/runbooks/upgrade-test.md) | Validate an upgrade before rollout |
| [rollback](../security/runbooks/rollback.md) | Revert a bad release |
| [incident](../security/runbooks/incident.md) | First-response for a suspected incident |
| [key-lifecycle](../security/runbooks/key-lifecycle.md) | Signing/stream key rotation + compromise |
| [relay-abuse](../security/runbooks/relay-abuse.md) | Respond to relay over-cap / abuse |
