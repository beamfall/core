## 1. Token tiers (CORE-2095)

- [x] 1.1 Add the `access` column migration and store support
- [x] 1.2 Carry the tier on the auth Scope and gate admin fields by tier
- [x] 1.3 Add `--access` to `beamfall token create` and the tier to `apiTokens` and `token list`

## 2. Catalog (CORE-2096)

- [x] 2.1 Add `agentCatalog` generated from the registries, with a completeness test
- [x] 2.2 Add the agent section to `docs/api/llms.txt`

## 3. Debug reads (CORE-2097)

- [x] 3.1 Add `serverLogs` filters
- [x] 3.2 Add `diagnosticsBundle` with bounded text profiles

## 4. MCP (CORE-2098)

- [x] 4.1 Add `beamfall mcp` with a stdio test against an httptest server

## 5. Verify and close

- [x] 5.1 Regenerate wire goldens, mgmt manifest and backup fixtures; publish wire contract 2.12.0
- [x] 5.2 Run focused tests, lint, a live probe and `openspec validate --all --strict`
