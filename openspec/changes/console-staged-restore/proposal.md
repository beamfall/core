# Stage a backup restore from the console

Ticket: CORE-1720 (`queue:core:main`).

## Why

Restoring a backup meant stopping the server and running `beamfall restore` on the host. An
admin could see the published backup sets in the console but could not tell what a set held, which
versions it carried, or whether this server could restore it, and could not start a restore without
a shell.

## What Changes

- New Admin-Session fields: `inspectBackup(envelope, backupKey)` opens an encrypted Backup Set with
  its backup key and reports its contents, versions and whether this server can restore it;
  `stageRestore(envelope, backupKey, replaceIdentity)` stages it; `cancelStagedRestore` drops it;
  `stagedRestore` reports the staged restore and the last applied one. The backup key is an
  argument only and is never stored, logged or returned.
- Staging decrypts the set into `<data-dir>/restore-staged` with a marker. Sets that are newer than
  this binary, or whose secrets and certificates differ from the server's without
  `replaceIdentity`, are refused with a clear reason.
- On the next `serve`, before the store opens, the staged set is applied through the same restore
  path as `beamfall restore`: the data directory is kept as `<data-dir>.pre-restore-<time>`, and a
  failed restore puts it back. The decrypted copy is always removed and the outcome is recorded.
  If the original cannot be put back, the server refuses to start.
- The console Backup page gains a restore panel for the selected set: backup key field, contents
  and versions, a replace-identity choice, stage, cancel, and the last result with its rollback
  copy. The offline steps stay below it.

## Non-goals

- Legacy plaintext backup sets stay CLI-only.
- No migration. The versioned wire-contract artifact is left to the batch publish.
