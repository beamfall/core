## 1. Server

- [x] 1.1 `internal/redact`: secret patterns moved from `internal/jobs`, plus attribute, home path and IP redaction
- [x] 1.2 `internal/logtail`: bounded ring and a slog text handler that tees into it
- [x] 1.3 Operator logger tees into the ring; the app hands the ring to the graph handler
- [x] 1.4 Admin-gated `serverLogs(limit)` query, registered and covered by a graph test
- [x] 1.5 Wire-contract goldens, field inventory and management manifests regenerated

## 2. Console

- [x] 2.1 Server logs page under Monitor with refresh and a plain-text bundle download
- [x] 2.2 Strings in `strings/server-logs.ts`, registered in the catalog and the registry baselines
- [x] 2.3 Vitest for lines, download bundle, no-tail and error states, and the GraphQL client
