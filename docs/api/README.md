---
name: developer-api-documentation
description: Human and AI entry points for Beamfall Core's versioned developer API.
updated: 2026-11-14
---

# Developer API documentation

- Human reference: [`reference.md`](reference.md)
- Home-automation examples: [`home-automation.md`](home-automation.md)
- Request-tool integration: [`request-tools.md`](request-tools.md)
- Compact AI reference: [`llms.txt`](llms.txt)
- Full AI reference: [`llms-full.txt`](llms-full.txt)
- Machine contract: [`openapi.json`](openapi.json)

`openapi.json` is the `/v1` contract source. After changing it, run `script/gen-api-docs.sh` from
the repository root and commit the regenerated reference and LLM files. Do not hand-edit generated
outputs.

A running server serves the same `openapi.json` at `GET /api/docs/openapi.json` and a browsable
reference rendered from it at `GET /api/docs`, with no credential. The served bytes are this file,
embedded at build time (`openapi.go`), so the live document is the one the contract checks validate.

`/api/graphql` is a private, server-authoritative client surface, not a stable third-party
integration contract. It may change without the `/v1` compatibility window. Third-party tools must
use the documented `/v1` OpenAPI surface. Internal-client maintainers can start with
[`../architecture/ARCHITECTURE.md`](../architecture/ARCHITECTURE.md) and ADR-0040 from the
`../adr/README.md` index.
