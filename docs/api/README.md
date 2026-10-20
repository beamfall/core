---
name: developer-api-documentation
description: Human and AI entry points for Beamfall Core's versioned developer API.
updated: 2026-07-12
---

# Developer API documentation

- Human reference: [`reference.md`](reference.md)
- Home-automation examples: [`home-automation.md`](home-automation.md)
- Compact AI reference: [`llms.txt`](llms.txt)
- Full AI reference: [`llms-full.txt`](llms-full.txt)
- Machine contract: [`openapi.json`](openapi.json)

`openapi.json` is the `/v1` contract source. After changing it, run `script/gen-api-docs.sh` from
the repository root and commit the regenerated reference and LLM files. Do not hand-edit generated
outputs.

`/api/graphql` is a private, server-authoritative client surface, not a stable third-party
integration contract. It may change without the `/v1` compatibility window. Third-party tools must
use the documented `/v1` OpenAPI surface. Internal-client maintainers can start with
[`../architecture/ARCHITECTURE.md`](../architecture/ARCHITECTURE.md) and ADR-0040 from the
`../adr/README.md` index.
