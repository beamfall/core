# Restore or roll back Beamfall offline

Restore is an offline host operation. The complete backup, verification, and retention runbook is
[Backup and restore](../security/runbooks/backup-restore.md).

## Choose the recovery path

- Use a verified Backup Set when recovering the server or moving back to a known-good full backup.
- Use a Rollback Snapshot only to return the databases to the state immediately before one upgrade
  session. It is a plaintext, database-only artifact under
  `<data-dir>/backups/pre-migration/<session-id>/manifest.json`; it is not a backup and does not
  contain secrets, TLS identity, or configuration.
- Never downgrade in place. Do not install or start an older Beamfall binary against a data
  directory that a newer binary may have migrated. Restore the pre-migration database tree while
  Beamfall is stopped, then start the selected prior binary.

## Roll back a migration

1. Select the exact Beamfall version that ran before the upgrade. Keep it stopped. Confirm the
   candidate manifest's `fromVersions` and `toVersions` tuple belongs to that upgrade session; the
   release manifest does not describe migrations, so do not infer a match from a release number or
   filename. If the prior binary version or session cannot be proved, stop and preserve the data
   directory for recovery.
2. Stop Beamfall and confirm the process has exited. The restore command refuses to proceed while
   `<data-dir>/server.lock` exists; do not remove a lock belonging to a running process.
3. Select a trusted pre-migration manifest from
   `<data-dir>/backups/pre-migration/<session-id>/manifest.json`. It must identify
   `type: "pre-migration"` and `restoreInstructionsVersion: 2`. Keep the whole session directory
   intact so its recorded database snapshots can be verified.
4. Run the guarded rollback command with the server still stopped:

   ```sh
   beamfall restore \
     --rollback-manifest /var/lib/beamfall/backups/pre-migration/<session-id>/manifest.json \
     --data-dir /var/lib/beamfall
   ```

   The command rejects a live server lock, a non-pre-migration or unsupported manifest, and any
   snapshot whose size or hash does not match the manifest. It stages and verifies the replacement
   before atomically replacing the database tree. The prior tree is retained as
   `db.pre-rollback-<timestamp>`.
5. Start the selected prior Beamfall binary. Verify liveness (`beamfall_up 1`), make an
   authenticated read, and compare expected library/media counts with the pre-upgrade record.
6. Keep the pre-rollback database tree and the trusted manifest until those checks succeed. If
   verification fails, stop Beamfall again and preserve both trees for recovery; do not attempt an
   in-place downgrade or hand-edit schema versions.

## Restore a verified Backup Set

For disaster recovery rather than migration rollback, follow the
[full restore runbook](../security/runbooks/backup-restore.md#restore). A v2 Backup Set contains
databases plus operational state and requires the separately held BackupKey. A legacy v1 backup is
database-only and requires secrets, TLS identity, and configuration to be re-provisioned. BackupKey
and Library Recovery Key material are never inside either artifact.
