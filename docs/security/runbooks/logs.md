# Runbook: Logs

Where Beamfall Core logs go and how to read them during an investigation.

## Procedure
1. The server logs to stdout/stderr (capture via the supervisor/container runtime).
2. For an incident, collect the time window around the event and correlate with `/metrics` deltas.
3. Audited mutations (deletes route through audited server mutations, inv. 6) are the authority for
   "what changed"; prefer them over inferring from request logs.
4. Every HTTP request produces one `http request` line at Info: `requestID`, `method`, `route`
   (the matched route pattern, or `unmatched`), `path` (Stream Token segments shown as
   `[redacted]`, never the query string), `status`, `latency_ms` and `principal` — the *kind* of
   credential presented (`anonymous`, `session`, `api_token`, `scoped_key`, `stream_token`), never
   its value. The same `requestID` is in the `X-Beamfall-Request-Id` response header, so a client
   report or a 5xx log line leads straight to its access line.
5. A refused credential produces one `auth refused` record at Warn that is written whatever the
   configured log level: `operation` (`credential`, `admin_session`, `integration_sign_in`,
   `enroll`, `setup`, `promote`), `outcome=refused`, `reason`, `credential` (the class), `method`,
   `route`, `status` and `requestID`. It never carries a session ID, device secret, token, key,
   password or pairing code. Scope-resolution refusals are recorded only when the response was 401
   or 403; explicit sign-ins (admin re-auth, integration sign-in, enrollment) are recorded on every
   refusal. Counts are in `beamfall_auth_refusals_total{reason}` (see [metrics](metrics.md)).

## Targets
- Log retention + shipping destination: keep local/supervisor logs for at least the 7 daily + 4 weekly
  backup window; external shipping is operator-owned and must stay opt-in/local-first.
- Required structured fields / redaction rules: include timestamp, severity, component, request/job
  correlation where available; never log secrets, stream tokens, Profile-private content, or telemetry
  that was not explicitly enabled.
