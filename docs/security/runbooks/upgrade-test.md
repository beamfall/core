# Runbook: Upgrade test

Validate an upgrade before rolling it out.

## Procedure
1. Take a fresh backup (see [backup-restore](backup-restore.md)).
2. Restore the backup into a staging instance on the candidate release.
3. Confirm migrations apply forward cleanly (migrations are append-only; never edit applied SQL).
4. Run `make gate` against the candidate, plus a smoke (health + representative read/write).
5. Confirm `/metrics` is healthy and error sentinels are 0 by presenting the rotating scrape
   credential described in [The scrape credential](metrics.md#the-scrape-credential):
   ```sh
   curl -sS -H "Authorization: Bearer $(cat <data-dir>/secrets/metrics-scrape-token)" \
     http://127.0.0.1:8800/metrics
   ```

## Targets
- Staging soak duration before prod rollout: run the restored staging smoke long enough to cover the
  migration and representative read/write path; pre-launch default is one clean smoke run plus operator
  review.
- Downgrade/forward-compat expectations: do not assume binary downgrade across schema changes; keep a
  pre-upgrade backup and restore it if rollback crosses an incompatible migration. ORG-38 adds
  release-downgrade smoke coverage.
