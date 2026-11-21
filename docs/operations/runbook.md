---
name: operations-runbook
description: Operator runbook index for the Beamfall Core server (deploy, observe, back up, recover).
updated: 2026-11-21
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
| [lockout-and-recovery](../security/runbooks/lockout-and-recovery.md) | Admin/integration lockout, admin password reset, first-run reset, database corruption |
| [key-lifecycle](../security/runbooks/key-lifecycle.md) | Signing/stream key rotation + compromise |
| [relay-abuse](../security/runbooks/relay-abuse.md) | Respond to relay over-cap / abuse |

## Container health after a replace

The image's `HEALTHCHECK` probes `GET /health` every 2s while the container starts, then every
30s. After a container is replaced, `docker inspect --format '{{json .State.Health}}'
Beamfall-Core` reports `starting` until the first probe passes. If that happens within 5 minutes,
the state turns `healthy` about 2s after `/health` first answers, and failures before it do not
count. After the first pass, 3 failed probes in a row mark the container `unhealthy`. If no probe
passes within 5 minutes, probes drop to every 30s and failures count, so the container can turn
`unhealthy` before it ever passes and take up to 30s to notice a late `/health`. The 2s start
interval needs Docker Engine 25 or later; an older engine probes at its default interval while
starting. `docker-compose.dev.yml` overrides only the probe's port and inherits this timing.

Images built before CORE-2164 had a 30s start period and Docker's default start interval of
5s. If the server took more than 30s to answer, probes fell back to every 30s, and the health
state could lag a passing `/health` by up to 30s. That is the likely cause of the late state
seen on Unraid on 2026-11-15; the probe timestamps from that replace were not kept. No dockerd
fault was found, and restarting the Docker service is not needed. A replace on 2026-11-21
(Docker 29.8.2) answered within the old start period and turned healthy in 10s: the probe at
+5s failed and the one at +10s passed. On a stub server that answers at +40s, the old timing
reported healthy at +61s and the new one at +42s (Docker 29.1.3).

To compare the two views after a replace, read Docker's events for the last 10 minutes and
probe the server directly. The events show the probes and health transitions Docker still
holds; it keeps only its most recent events, so for a complete timeline start `docker events`
before the replace. `docker inspect` keeps only the last 5 probe results, which at a 2s cadence
covers about 10 seconds, so capture it right away if you need those results.

```sh
docker events --since 10m --until "$(date +%s)" --filter container=Beamfall-Core --format '{{.Time}} {{.Action}}'
docker inspect --format '{{json .State.Health}}' Beamfall-Core
curl -fsS http://127.0.0.1:8800/health
```
