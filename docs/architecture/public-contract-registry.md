# Public contract registry (CORE-0902)

Status: proposed, 2026-11-13. Implementation waits on review of this design.

## Problem

`beamfall/core` is public. Its `contracts/registry.json` still carries fleet bookkeeping that
belongs to private repositories:

- `client-core-wire` consumer pins and `consumerArtifacts` paths inside private client repos;
- eight `knownDrift` records, each with an internal ticket id and a deadline;
- two boundaries that core neither produces nor consumes (`entitlement-vectors`,
  `web-cloud-accounts`, both produced by the private cloud service);
- `conformanceTest` text that names test files in private repos.

The superseded wire artifacts `internal/conformance/wire-contract-v1.2.0.json` through
`v1.15.0.json` each embed one drift record (`XCONTRACT-1b`, deadline `2026-11-30`). The
artifacts `wire-contract-v2.0.0.json` (pinned by every client consumer) and `wire-contract-v2.1.0.json` (active since core `43bbe3ea`), plus `tools/wire-contract/testdata/wire-contract.json`,
carry `knownDrift: null` and are already clean.

This material is low sensitivity: repo names, ticket ids and dates, with no credentials or
customer data. It has been public since the 2026-10-31 flatten. This change stops new
accumulation. It cannot recall what is already published.

## Decision

Core's public registry records **producer facts only**. Consumer bookkeeping lives in the
workspace registry (`beamfall-workspace/contracts/registry.json`, private), which already
holds the `client-core-wire` pins and runs `consumer-pin-truth.js` and deadline checks.

Public registry row, after the change:

| Field | Rule |
|---|---|
| `boundary`, `producer`, `artifact`, `version`, `activeArtifactVersion`, `artifactSHA256`, `license` | unchanged |
| `consumers` | repo names only (strings), no `pinnedVersion` objects, for every boundary including `client-core-wire` |
| `consumerArtifacts` | removed |
| `conformanceTest` | core-side tests only; private-repo test paths removed |
| `knownDrift` | always `[]` in core; drift records move to the workspace registry |

Boundaries core neither produces nor consumes (`entitlement-vectors`, `web-cloud-accounts`)
leave core's registry. The workspace registry already has `web-cloud-accounts`, and gains the
entitlement row under its existing `entitlement-money-path` boundary or a new one.

`relay-core-tunnel` (producer `beamfall-relay`, public) and `plugin-artifact-trust` (core is
the consumer; the artifact lives in core's testdata) stay as they are.

Superseded wire artifacts v1.2.0–v1.15.0 are immutable published identities, so their bytes
are not rewritten. Nothing pins them: every client consumer pins 2.0.0 (the active artifact is 2.1.0, a permitted minor skew), and
`newestWireContractArtifact` reads only the newest file. They move out of the public tree into
`core-private/` as preserved originals for provenance and rollback. `v1.0.0` and `v1.1.0` hold
no bookkeeping and stay. The pinned 2.0.0 and active 2.1.0 artifacts and their byte pins do not change, so no fresh
artifact identity is needed.

## Checks: where each one runs after the change

| Check | Today | After |
|---|---|---|
| Required keys, non-empty `consumers` | `script/check-contract-registry.py` | unchanged |
| `client-core-wire` pin shape and major-skew rule (ADR-0122 Amendment A §1) | `wire_contract_test.go` reads core registry pins | workspace `consumer-pin-truth.js` against the workspace rows. Core keeps the published-artifact freshness checks |
| Consumer truth: sibling repos really vendor the pinned bytes | `check-contract-registry.py` `_wire_consumer_truth_failures`, `consumerArtifacts` | workspace gate. The `consumerArtifacts` map moves with the pins |
| `knownDrift` ticket and deadline expiry | core checker | workspace `check-registry.js` (already enforces deadlines). The core checker keeps validating any drift entry it sees, but the absence test below keeps core's list empty |
| `crossrepo-conformance.sh` web pin | reads core registry | reads the workspace registry via `BEAMFALL_WORKSPACE_ROOT` and fails closed when it is missing. The script needs sibling checkouts anyway |
| Artifact knownDrift equals registry knownDrift | `wire_contract_test.go:171-194` | unchanged (both `[]`/null) |

New tests:

- **Absence (core):** the public registry has no `pinnedVersion`, `consumerArtifacts`,
  `deadline` or non-empty `knownDrift`. No boundary has a private-only producer that core
  neither produces nor consumes. No `wire-contract-v*.json` in the tree has a non-null
  `knownDrift`.
- **Retention (workspace):** every consumer pin, `consumerArtifacts` path and drift record
  removed from core exists in the workspace registry with the same values. The workspace
  ticket writes this test, and it runs under the workspace contract gate.
- **Equivalence:** the 2.0.0 and 2.1.0 wire artifacts, `tools/wire-contract/testdata/*` and
  `plugin-trust/v1` keep their SHA-256 values byte for byte. Structural schema equivalence
  holds trivially because no artifact bytes change.

## Migration order (consumer-first, CORE-1339 rule)

1. **Workspace ticket CORE-1428** (its own owner and contract review): add the consumer rows, pins,
   `consumerArtifacts`, drift records and the entitlement row to the workspace registry. Add
   the retention test. Run `make contracts-fmt && make gate`. This is harmless on its own,
   because core still carries the same data.
2. **Core (this ticket):** strip the core registry, update `check-contract-registry.py`,
   `check_contract_registry_test.py`, `wire_contract_test.go`, `crossrepo-conformance*.sh`
   and its tests. Move the v1.2–v1.15 artifacts to `core-private/`. Add the absence tests.
   Gate on the exact head, against fresh `origin/main` sibling worktrees.
3. No consumer repo changes. Client repos read their own vendored wire bytes, not core's
   registry.

## Failure modes

- **A private check silently stops running.** The retention test plus a workspace gate run in
  step 1 prove the checks run before core drops its copy. The core change lands only after the
  workspace change is on `origin/main`.
- **`crossrepo-conformance.sh` runs without a workspace** (a public clone). It fails closed
  with a message naming `BEAMFALL_WORKSPACE_ROOT`. It never passes on missing data.
- **Drift is recorded in core again.** The absence test fails the core gate.
- **A tool still reads the v1.x artifacts.** `git grep` for `wire-contract-v1.` before the
  move. Known readers: `wire_contract_test.go` (synthetic temp files only),
  `check_contract_registry_test.py`, `crossrepo-conformance_interrupt_test.py`,
  `crossrepo-conformance_pin_test.sh`, `tools/gate/gate_scope_test.sh`. Each one is checked
  and, where it uses real files, switched to a fixture.
- **Schema drift between the registries.** Core schema 1 vs workspace 1.1.0. The retention
  test compares values per field, not whole rows.

## Rollback

Revert the core commit. The workspace rows from step 1 are additive and harmless next to a
restored core registry. The preserved v1.x originals in `core-private/` restore byte for byte.

## Out of scope

- Recalling what is already public (not possible).
- Publishing anything. No visibility changes happen in this ticket.
- Consumer repins (none are needed).
