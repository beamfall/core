# Beamfall Core documentation

Pick the shortest path for what you need. These pages link to the canonical material rather than
restating it.

## Run and operate Core

- [Build and run locally](../README.md#build--test)
- [Operations runbook](operations/runbook.md)
- [Restore](operations/restore.md)
- [Deploy](security/runbooks/deploy.md)
- [Back up and restore](security/runbooks/backup-restore.md)
- [Metrics and alerts](security/runbooks/metrics.md)
- [Incident response](security/runbooks/incident.md)
- [Security policy](../SECURITY.md)

## Integrate with Core

- [Developer API documentation](api/README.md)
- [Home-automation examples](api/home-automation.md)
- [GraphQL and package architecture](architecture/ARCHITECTURE.md)
- [Plugin SDK and authoring documentation](https://github.com/beamfall/plugin-sdk)
- [Writing a scraper](plugins/writing-a-scraper.md)

Generated machine-readable API references live beside the API reference as `llms.txt` and
`llms-full.txt`; human readers should normally start with [`reference.md`](api/reference.md).

## Understand the system

- [Architecture and dependency rules](architecture/ARCHITECTURE.md)
- [Testing architecture](architecture/TESTING.md)
- [Domain-language index](../CONTEXT.md)
- [Glossary](glossary/core-platform-entities.md)

## Product and interface contracts

- [Design-system contract](design-system.md)
- [Per-surface design contracts](design/)
- [Accessibility contract](design/accessibility-contract.md)

## Contribute

- [Contribution guide](../CONTRIBUTING.md)
- [Repository rules for coding agents](../AGENTS.md)
- [Testing expectations](architecture/TESTING.md)
- [Verification Claims Register](security/CLAIMS.md)
