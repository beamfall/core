# Operator agents

An AI agent can inspect and run a Beamfall server for its operator through the private
`/api/graphql` surface. This is not the `/v1/` developer contract: field shapes follow the server
release, and every credential below is operator authority.

## Credentials

Mint a token on the host while the server is stopped. A token is shown once; keep it in a `0600`
file or a secret store.

```sh
beamfall token create --name my-agent --access diagnostics
```

| Tier | Runs |
|---|---|
| `cli` | the operator CLI fields: `scanLibrary`, `libraries`, `runJob`, `backup`, `apiTokens`, `revokeApiToken` |
| `diagnostics` | `cli`, plus every admin read and `jobCancel` / `jobRetry` |
| `agent` | every admin field except `createApiToken`, `createApiKey`, `registerImportSource` and `transferProfileOwner` |

No GraphQL operation creates an elevated token or changes a tier. `beamfall token list` shows each
token's tier; `beamfall token revoke` withdraws one.

## Discover

```graphql
query { agentCatalog { guide fields { name kind adminSession tokenTiers argumentsKnown arguments resultFields } } }
```

The catalog lists every top-level field, whether it needs an Admin Session, the token tiers that may
run it, its arguments and its top-level result fields. It comes from the dispatcher registries, so it
matches what the server enforces. A refused field answers `admin session required`. Each
argument reads `name: kind`, with `optional` when the field runs without it; an empty list means the
field takes no arguments. A conformance test keeps the list in step with what the resolvers read.

## Debug

- `serverLogs(limit, contains, level, requestId)` reads the redacted in-memory log tail. Every
  response carries an `X-Beamfall-Request-Id` header; pass it as `requestId` to find that request's
  lines.
- `diagnosticsBundle(profiles: [...], limit, contains, level, requestId)` returns the version, build
  revision, uptime, goroutine count, memory statistics, filtered log lines and the text form of the
  requested runtime profiles (`allocs`, `block`, `goroutine`, `heap`, `mutex`, `threadcreate`). Each
  profile is capped at 256 KiB.
- `GET /health` and `GET /metrics` need no credential.

## MCP

`beamfall mcp` runs a Model Context Protocol server over stdio that proxies to a running server:

```sh
BEAMFALL_SERVER=http://127.0.0.1:2326 BEAMFALL_TOKEN="$(cat ~/.beamfall-agent-token)" beamfall mcp
```

It offers the tools `catalog`, `graphql`, `logs`, `bundle` and `health`, and never prints the token.
