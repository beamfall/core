# Runbook: Incident response

First-response for a suspected security or availability incident.

## Procedure
1. **Triage:** confirm scope from `/metrics` deltas + logs ([logs](logs.md), [metrics](metrics.md)).
2. **Contain:** if a key may be compromised, follow [key-lifecycle](key-lifecycle.md); if relay
   abuse, follow [relay-abuse](relay-abuse.md).
3. **Preserve:** snapshot the DB ([backup-restore](backup-restore.md)) and capture the log window
   before mutating state.
4. **Eradicate / recover:** patch or roll back ([rollback](rollback.md)); restore from backup if data
   integrity is in question (trash + audited mutations are the recovery substrate, inv. 6).
5. **Report:** a confirmed high+ severity issue is release-blocking (see `SECURITY.md`); record a
   timeline and follow-ups.

## Targets
- Severity bands: CVSS v3.1 critical >= 9.0, high 7.0-8.9, medium 4.0-6.9, low < 4.0; high and
  critical issues are release-blocking until fixed or mitigated.
- Acknowledgement SLA: high/critical within 1 business day; medium/low within 3 business days.
- Disclosure timeline + comms owner: maintainers route through `security@beamfall.app`; publish only
  after a fix or accepted mitigation is available.
