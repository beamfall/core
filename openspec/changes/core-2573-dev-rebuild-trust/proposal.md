# A rebuilt debug client keeps its device

Ticket: CORE-2573 (`queue:core:main`).

## Why

The operator's runtime dev-build file (`BEAMFALL_DEV_BUILDS_FILE`, CORE-2396) pins one artifact
digest per `-dev` build id. A debug build's digest changes with every rebuild, so each rebuild of
the owner's own debug client is refused as `client-too-old` / `unknown-build` (server finding
`artifact-hash-mismatch`) until the file is hand-edited on the server. On 2026-11-18 a Pixel running
`android-viewer-mobile-0.1.0-600-dev` was locked out twice in 15 minutes.

## What Changes

- Records loaded from the dev-build file are marked as dev records. A linked release record never is.
- Build provenance accepts a presented digest that differs from a dev record's digest only when the
  enrollment already holds the exact mapping to that surface and build id, which it can get only by
  verifying that build once under the operator-cataloged digest. The presented digest must still be
  well formed. Every other check is unchanged: a known unambiguous record, surface, pinned device
  surface, rollback floor, role, distribution and exact mapping.
- Creating a mapping (enrollment, and the CORE-1393 handshake rebind) still needs the exact digest,
  so a rebuilt digest never creates the mapping that would then admit it. A new device or a new
  build id still needs the operator's record once.
- The dev flag is part of the catalog digest, so it advances the decision generation.

## Security

ADR-0063 already treats the artifact digest as a bearer claim and accepts the residual risk of a
substituted artifact presenting a known digest while enrollment is limited to operator-managed
devices. A device that holds the mapping could present the cataloged digest anyway, so relaxing the
digest for that device's `-dev` build grants nothing beyond the device credential the operator
paired. Unknown and third-party builds stay refused, release records stay exact, and removing the
record from the file revokes every device within the file's re-read interval, with no restart.

## Non-goals

- An owner "Trust this build" action in Console → Access (a possible follow-up).
- Changing the dev-build file format, the release catalog or the rollback floor.
