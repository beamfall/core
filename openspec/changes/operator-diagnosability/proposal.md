# Operator diagnosability for the HTTP server

Ticket: CORE-0733 (`queue:core:main`).

## Why

An operator cannot see what the server is doing from its own output. The server logs a request
only when it answers 5xx, and `/metrics` carries no request counter, so the documented "HTTP 5xx >
1%" alert cannot be computed. A refused credential (a bad device secret, an expired session, a
locked admin re-auth, a wrong pairing code) leaves no log line, metric or audit record. `/health`
says `"status":"ok"` next to `ready:false` and says nothing about FFmpeg. The admin lockout,
`beamfall reset-admin-password`, first-run reset and database-corruption recovery are in no
operator runbook.

## What Changes

- One middleware around the whole request emits a structured `http request` line for every
  request: request ID, method, matched route pattern, redacted path (no query string), status,
  latency and the class of credential the caller presented. It never logs a credential value.
- Every response, including early refusals (access policy, base URL), carries the
  `X-Beamfall-Request-Id` header.
- `/metrics` reports `beamfall_http_requests_total{status_class}` for `1xx`..`5xx`, plus
  `beamfall_http_request_duration_seconds_sum` and `_count`. Labels are bounded: no path, route,
  principal or identifier label.
- `internal/auth` notes why it refused a presented credential, from a closed reason vocabulary.
  The server turns a noted refusal into one `auth refused` audit record per request, which
  bypasses the log-level filter, and counts it in `beamfall_auth_refusals_total{reason}`. The
  record carries the operation, reason, credential class, method, route and request ID; it never
  carries a session ID, device secret, token, key, password or pairing code.
- `/health` derives `status` from `ready` (`ok` or `not_ready`) and reports whether `ffmpeg` and
  `ffprobe` resolve on the server's `PATH`. FFmpeg presence does not change readiness.
- A new runbook, `docs/security/runbooks/lockout-and-recovery.md`, covers admin and integration
  lockout, `beamfall reset-admin-password`, first-run reset, database corruption and a
  `LimitNOFILE` recommendation. `docs/security/runbooks/metrics.md` states what
  `--public-metrics` really does and names the new request counters.

## Non-goals

- Changing the log level at runtime. The ticket asks for it through the existing console config
  path, which lives in `internal/graph` and the console config store; another lane owns
  `internal/graph`, so it is left out of this change.
- Changing `--public-metrics` behaviour or its help text. The help text lives in
  `internal/config`, outside this change's paths; keeping the bearer required would reverse an
  operator decision. The runbook documents the real behaviour instead.
- Gating readiness on FFmpeg. A server without FFmpeg still serves metadata, Direct Play and the
  console, so it stays ready; the new fields make the gap visible.
- Shipping logs or metrics anywhere. Everything stays on the host.

## Failure modes

- Log volume rises by one line per request at `info`. Operators who do not want it raise the
  level to `warn`; audit records still appear because they bypass the filter.
- A credential refusal on a request that still succeeds through another path (for example a
  compat route) is not recorded: scope-resolution refusals are recorded only when the response is
  401 or 403. Explicit sign-in attempts (admin re-auth, integration sign-in, enrollment) are always
  recorded.

## Acceptance evidence

- `internal/server` tests: the access line fields and redaction, the request counters, the
  `auth refused` record for a bad device secret and a wrong pairing code with a canary secret that
  never reaches the logs or `/metrics`, and the `/health` status and FFmpeg fields.
- `internal/auth` tests: refusal reasons noted for each refused credential shape, and nothing
  noted without a collector or on success.

## Rollback

Revert the change. No stored state depends on it.

## Capabilities

### New Capabilities

- `operator-diagnosability`: the access log, request and refusal metrics, auth refusal audit
  records and `/health` fields an operator uses to diagnose the server.

### Modified Capabilities

None.
