# Runbook: Logs

Where Beamfall Core logs go and how to read them during an investigation.

## Procedure
1. The server logs to stdout/stderr (capture via the supervisor/container runtime).
2. For an incident, collect the time window around the event and correlate with `/metrics` deltas.
3. Audited mutations (deletes route through audited server mutations, inv. 6) are the authority for
   "what changed"; prefer them over inferring from request logs.

## Targets
- Log retention + shipping destination: keep local/supervisor logs for at least the 7 daily + 4 weekly
  backup window; external shipping is operator-owned and must stay opt-in/local-first.
- Required structured fields / redaction rules: include timestamp, severity, component, request/job
  correlation where available; never log secrets, stream tokens, Profile-private content, or telemetry
  that was not explicitly enabled.
