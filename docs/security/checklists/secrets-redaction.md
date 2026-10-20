# Secrets Redaction Checklist

Any release, support, CI, or E2E artifact that can leave the operator machine must prove the secret
classes below are absent or redacted.

| Secret class | Artifact surfaces to check |
|---|---|
| Pairing and enrollment | pairing codes, one-time bootstrap tokens, device secrets, enrollment URLs in logs, response bodies, screenshots, CI output, and support bundles |
| Sessions and API access | `Authorization` headers, session cookies, API keys, scoped tokens, Admin Session proof, Stream Tokens, playback URLs, and SSE reconnect URLs |
| Plugin and egress credentials | OAuth codes, refresh tokens, scraper credentials, webhook secrets, qBittorrent credentials, outbound endpoint auth, and validation failure bodies |
| Backup and encryption | Recovery Keys, wrapped DEKs, backup bucket credentials, hosted backup tokens, KMS/key IDs where sensitive, and restore/export artifacts |
| Private library metadata | restricted Library names, hidden titles, source paths, private endpoints, remote hostnames, and Profile-specific no-access counts |

Required proof:

- Automated tests inject representative values for every class and fail if any value appears in logs,
  response bodies, support bundles, CI artifacts, exports, database debug dumps, or crash reports.
- Redaction is applied before persistence where possible; otherwise before artifact export.
- No release checklist row may treat health/metrics coverage as sufficient redaction evidence.

## Implemented redaction gates (ADR-0013/ADR-0076)

| Layer | Mechanism | Test |
|---|---|---|
| HTTP request logs (`logRequestFailure`) | Logs `r.URL.Path` only — never `RawQuery`; no header values logged | `TestWriteErrorCauseLogsMethodPathAndPrivateCause` |
| HTTP ops correlation | Per-request ID generated at `ServeHTTP`; written to `X-Beamfall-Request-Id` header and logged on 5xx so operators can join client report to log line | `TestServeHTTPAttachesRequestIDCorrelation` |
| Job failure log + persistence (`jobs.RunOne`) | `redactLogError` scrubs API keys (`bfk_`), Bearer/Basic tokens, `?token=` params, stream/pack URL paths, and private endpoint URLs before the string reaches `slog` or durable failed-job state | `TestRedactLogErrorScrubsSecretClasses`, `TestRunOneDoesNotLogOrPersistSecretInJobFailureDetail` |
| Job payload (GraphQL `jobEvents`) | Graph layer omits `Payload` for restricted-target jobs (redact flag from `jobNeedsRedaction`); Console renders nil payload as `jobLogPayloadRedacted` placeholder and redacts secret-bearing non-nil payload text before render | `TestJobsQueryRedactsRestrictedLibraryErrorsUntilRevealed`, `TestRunJobCancelRetryRecordsDurableEvents` (graph package), `OperationsControls` live-log payload test |
| Release journey | End-to-end scenarios prove: response carries unique request ID; secrets in query/headers never appear in log; IDs are unique across requests; request/job/SSE/Console live-log redaction is tied together | `TestOpsReleaseJourneyRedactionAndCorrelation`, `TestOpsReleaseJourneyRequestJobAndConsoleLiveLogRedaction`, `TestOpsUnrestrictedJobFailureRedactsConsoleVisibleState` |
