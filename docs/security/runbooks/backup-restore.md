# Runbook: Backup and restore

Canonical operator procedure for snapshotting and recovering Beamfall Core. Restore is an offline
host action; stop Core before starting.

## Create and verify a backup

1. Run the Console backup action or the admin `backup(dest)` mutation. In the Console, select the
   destination through the `backupDestination` path picker. API callers must choose a destination
   under `<data-dir>` or another configured allowed root; Core rejects paths outside that sanctioned
   allowlist. See [Path configuration](../../glossary/path-configuration.md).
   Core writes a `beamfall-<timestamp>Z/` set containing consistent SQLite snapshots and
   `manifest.json`.
2. Use `backupHistory(dest)` to confirm the set is listed with the expected creation time and size.
3. Run `verifyBackup(manifest)` to re-check the recorded size and SHA-256 of every snapshot.
4. Copy verified sets off-host. Encrypted-library databases remain ciphertext in the snapshot.
   Current manifests record absolute snapshot paths, so recovery must copy the complete set back to
   its original absolute set path before running `beamfall restore`. Location-independent moved-set
   restore is not yet a supported claim.

The built-in `database-backup` schedule runs daily by default. Retention pruning removes only older
backup copies, never the live data directory or server-side trash, and always preserves at least one
set.

## Restore

1. Stop Beamfall. The command refuses to run while `<data-dir>/server.lock` exists.
2. Restore the verified manifest:

   ```sh
   beamfall restore --manifest /path/to/backup/manifest.json --data-dir /var/lib/beamfall
   ```

3. Before touching the data directory, the command verifies manifest instructions, snapshot sizes
   and hashes, and that every database schema version is supported by this binary. Newer or corrupt
   backups fail closed.
4. If the data directory is non-empty, the command first renames it to a sibling
   `.pre-restore-<timestamp>` snapshot, then restores into a fresh directory. A partial failed restore
   is removed rather than left looking bootable.
5. Apply the pre-0060 credential scrub below when relevant, then start Beamfall.
6. Confirm `beamfall_up 1`, run an authenticated read query, and compare
   `beamfall_library_media_total` with the expected snapshot. Keep the pre-restore snapshot until
   verification succeeds.

### Pre-0060 backup scrub (dead `auth_header` column)

Snapshots taken **before** catalog migration 0060 may contain plaintext private-feed credentials in
`subscription.auth_header` — the literal `Authorization` header value (`Basic …` / `Bearer …`) stored
by the pre-0030 code path. Migration 0060 moved credentials to the `0600` secure-store keyed by
`auth_ref` and stopped writing `auth_header`, but the column is append-only (`never`-class
migrations) so it is **retained, not dropped**, and old rows still carry the secret.

When restoring a pre-0060 snapshot, scrub the column before bringing the instance online. With the
server still stopped and `sqlite3` available, run this transaction against the restored database:

```sh
sqlite3 '<data-dir>/db/catalog.db' <<'SQL'
BEGIN IMMEDIATE;
UPDATE subscription SET auth_header = '' WHERE auth_header <> '';
SELECT count(*) FROM subscription WHERE auth_header <> '';
COMMIT;
SQL
```

The query must print `0`. Any nonzero result or SQLite error leaves the restore unverified; do not
start Core.

Post-0060 instances always have an empty `auth_header` (the value lives in the secure store), so no
scrub is needed there.

## Shipped retention and recovery objectives

- Shipped schedule: one daily `VACUUM INTO` backup; successful runs keep the newest 14 published
  sets and prune older copies.
- Objective: RPO <= 24h and RTO <= 1h from a verified snapshot.
- Accepted future retention contract: ADR-0192 specifies the last 7 dailies, every seventh set to
  approximately 90 days, with a hard cap of 12. Do not describe that age+count policy as shipped
  until its gate exists.
- Restore-verification test: run a restore into a clean data dir, confirm `beamfall_up 1`, and compare
  `beamfall_library_media_total` against the expected snapshot. ORG-38 expands this into migration
  snapshot-restore coverage.
