# Runbook: Rollback

Revert a bad release.

## Procedure
1. Stop routing traffic to the affected instance.
2. Redeploy the last-known-good release (pin by digest/tag).
3. If schema migrated forward and the old binary cannot read the new schema, restore the
   pre-upgrade backup (see [backup-restore](backup-restore.md)) — do NOT hand-edit applied
   migration SQL (never-edit class).
4. Confirm `beamfall_up 1` and error sentinels 0 by presenting the rotating scrape credential
   described in [The scrape credential](metrics.md#the-scrape-credential), then make a representative
   read query:
   ```sh
   curl -sS -H "Authorization: Bearer $(cat <data-dir>/secrets/metrics-scrape-token)" \
     http://127.0.0.1:8800/metrics
   ```
5. File an incident note (see [incident](incident.md)) capturing cause + timeline.

## Targets
- Rollback decision SLA: declare rollback or mitigation within the 1h RTO target when availability or
  data integrity is affected.
- Forward-only migration policy for irreversible changes: applied migration SQL is append-only; if an
  old binary cannot read the new schema, restore the pre-upgrade snapshot rather than editing applied
  migrations.
