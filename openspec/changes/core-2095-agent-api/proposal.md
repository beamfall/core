# Agent API: tiered admin tokens, a self-describing catalog, debug reads and an MCP server

Ticket: CORE-2095 (`queue:core:main`), with siblings CORE-2096 (catalog), CORE-2097 (debug reads)
and CORE-2098 (`beamfall mcp`).

## Why

An LLM agent operating a running server cannot do much today. An admin API token (`bft_`) runs only
5 of the ~110 admin GraphQL fields; everything else answers `admin session required`, and the only
way to get an Admin Session is the browser flow (cookie, CSRF, password re-check every 15 minutes,
compat handshake every 30 seconds). `__schema` is refused and nothing lists the GraphQL fields, their
gates or their arguments. The redacted log tail (CORE-1716) cannot be filtered by request ID or level,
and there is no runtime or profile read.

## What Changes

- **Token access tiers.** `api_token` gains an `access` column (append-only migration). Tiers:
  - `cli` (default; every existing token): today's allowlist.
  - `diagnostics`: `cli` plus every admin read field, plus `jobCancel` and `jobRetry`.
  - `agent`: every admin field except `createApiToken`, `createApiKey` and `transferProfileOwner`.
  Elevated tiers are minted only offline with `beamfall token create --access diagnostics|agent`
  while the server is stopped, the same authority that mints any token today. No GraphQL operation
  mints a token or raises a tier, so a token still never mints a credential. `apiTokens` and
  `beamfall token list` report each token's tier.
- **Catalog.** A new `agentCatalog` query returns every top-level GraphQL field with its kind
  (query or mutation), whether it needs an Admin Session, and the token tiers that may run it, plus a
  short agent guide. It is generated from the dispatcher registries. Admin Sessions and every token
  tier may read it. `docs/api/llms.txt` gains an agent section.
- **Debug reads.** `serverLogs` gains `contains`, `level` and `requestId` filters. A new
  `diagnosticsBundle` admin read returns build, uptime, Go runtime and memory stats, recent filtered
  log lines, and on request the text form of named runtime profiles (`goroutine`, `heap`, `allocs`,
  `block`, `mutex`, `threadcreate`) — pprof in a form an agent can read.
- **MCP.** `beamfall mcp` speaks the Model Context Protocol over stdio and proxies to a running
  server with `BEAMFALL_SERVER` and `BEAMFALL_TOKEN`. Tools: `catalog`, `graphql`, `logs`,
  `bundle`, `health`. No new module dependencies.

## Non-goals

- No new HTTP routes or access classes; everything rides `/api/graphql`.
- No binary pprof or CPU profiling endpoint.
- No change to Admin Session rules for browsers, and no change to scoped developer keys or `/v1`.
