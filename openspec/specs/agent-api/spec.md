# agent-api Specification

## Purpose
Let an AI agent discover, inspect and operate a Beamfall server for its operator through the private `/api/graphql` surface, using a tiered API token that can never mint or elevate credentials.

## Requirements

### Requirement: Admin API tokens carry an access tier

Every admin API token SHALL carry one access tier from the closed set `cli`, `diagnostics` and
`agent`. A token created without a tier, and every token that existed before tiers, SHALL be `cli`.

#### Scenario: Existing token after upgrade

- **WHEN** a token minted before tiers existed queries `jobs`
- **THEN** the request fails with `admin session required`, as before

### Requirement: A token's tier bounds the admin fields it runs

A `cli` token SHALL run only the admin fields it ran before tiers. A `diagnostics` token SHALL also
run every admin read field and `jobCancel` and `jobRetry`. An `agent` token SHALL run every admin
field except `createApiToken`, `createApiKey`, `registerImportSource` and `transferProfileOwner`. A request that names any
admin field its token's tier does not allow SHALL fail with `admin session required`.

#### Scenario: Diagnostics token reads jobs

- **WHEN** a `diagnostics` token queries `jobs`
- **THEN** the server answers with the job list

#### Scenario: Diagnostics token runs an admin mutation

- **WHEN** a `diagnostics` token calls `setConsoleConfig`
- **THEN** the request fails with `admin session required`

#### Scenario: Agent token mints a credential

- **WHEN** an `agent` token calls `createApiToken` or `createApiKey`
- **THEN** the request fails with `admin session required`

### Requirement: Elevated tiers are minted offline only

`beamfall token create` SHALL accept `--access cli|diagnostics|agent`, defaulting to `cli`, and SHALL
refuse any other value. It SHALL keep refusing to run while the server holds the data directory.
The Admin Session `createApiToken` mutation SHALL create only `cli` tokens, and no GraphQL operation
SHALL change a token's tier. `apiTokens` and
`beamfall token list` SHALL report each token's tier.

#### Scenario: Mint an agent token

- **WHEN** the operator runs `beamfall token create --name probe --access agent` on a stopped server
- **THEN** a token is printed and `apiTokens` later lists it with access `agent`

#### Scenario: Unknown tier

- **WHEN** the operator runs `beamfall token create --name probe --access root`
- **THEN** the command fails naming the allowed tiers and creates no token

### Requirement: The GraphQL surface describes itself

The `agentCatalog` query SHALL list every top-level GraphQL field with its name, kind (`query` or
`mutation`), whether it requires an Admin Session, and the token tiers that may run it, and SHALL
include a short guide for agents. The list SHALL be derived from the dispatcher registries. An Admin
Session and a token of any tier SHALL be able to read it.

#### Scenario: Agent discovers what it may run

- **WHEN** a `diagnostics` token queries `agentCatalog`
- **THEN** `jobs` is listed as a query requiring an Admin Session with tiers `diagnostics` and
  `agent`, and `createApiToken` is listed with no tiers

### Requirement: Agents filter the server log

`serverLogs` SHALL accept optional `contains`, `level` and `requestId` filters and return only kept
lines that match all given filters.

#### Scenario: Find a failed request

- **WHEN** an agent queries `serverLogs(requestId: "abc")` with the ID from an
  `X-Beamfall-Request-Id` response header
- **THEN** only the kept lines carrying that request ID are returned

### Requirement: Agents read runtime diagnostics

The `diagnosticsBundle` query SHALL require an Admin Session or a
`diagnostics` or `agent` token, and SHALL return the build, uptime, Go runtime and memory statistics,
recent log lines under the same filters, and the text form of each requested runtime profile from
the closed set `goroutine`, `heap`, `allocs`, `block`, `mutex` and `threadcreate`. Each profile text
SHALL be bounded in size. Log lines SHALL stay redacted as the log tail keeps them.

#### Scenario: Goroutine dump

- **WHEN** an `agent` token queries `diagnosticsBundle(profiles: ["goroutine"])`
- **THEN** the response carries the goroutine stacks as text

#### Scenario: Unknown profile

- **WHEN** a caller asks for profile `cpu`
- **THEN** the request fails naming the allowed profiles

### Requirement: An MCP server exposes the agent API

`beamfall mcp` SHALL speak the Model Context Protocol over stdio, answering `initialize`,
`tools/list` and `tools/call`, and SHALL proxy each tool call to the server named by `--server` or
`BEAMFALL_SERVER` with the token from `--token` or `BEAMFALL_TOKEN`. It SHALL offer the tools
`catalog`, `graphql`, `logs`, `bundle` and `health`. A server error SHALL come back as a tool error
carrying the server's message. It SHALL never write the token to stdout or stderr.

#### Scenario: Agent lists tools

- **WHEN** an MCP client sends `initialize` then `tools/list`
- **THEN** the five tools are listed with JSON input schemas

#### Scenario: Refused operation

- **WHEN** a `diagnostics` token calls the `graphql` tool with a `setConsoleConfig` mutation
- **THEN** the tool result is an error carrying `admin session required`
