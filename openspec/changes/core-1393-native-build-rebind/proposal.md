# Rebind a native device's verified build at handshake

Ticket: CORE-1393 (`queue:core:main`).

## Why

`device_build_binding` (catalog migration 0112) maps an enrolled device to the first-party native
build it enrolled with (ADR-0063 build provenance). Only `POST /api/enroll` writes that mapping.
When an enrolled device installs a newer shipped build, its compatibility handshake presents a
build id with no mapping for its enrollment, so provenance reports `enrollment-build-mismatch` and
the handshake is refused as `client-too-old` / `unknown-build`. Only re-pairing restores service.
Devices enrolled before migration 0112 have no mapping at all and are refused the same way.

## What Changes

- A compatibility handshake made with a native device credential, meaning the device's own session,
  checks whether the presented build is mapped for that device and surface. If it isn't, the server runs
  `EnrollmentBindingFor` against the device's server-held enrollment (role, distribution class, the
  device surface already bound). This is the same verification enrollment uses: a known, usable catalog
  record, the surface, the artifact digest, the rollback floor, and role and distribution entitlement.
  If the build verifies, the device's mapping for that surface is replaced, and the handshake is then
  evaluated against the new mapping.
- The catalog's rollback floor is the downgrade bound. A device may move back to an older verified build
  that is still at or above the floor. That lets a device that upgraded to a build its server can't serve
  yet reinstall the build it ran before, without re-pairing. Below the floor, nothing is recorded.
- A verified build is recorded even when the handshake is then refused for another reason, such as
  needing a newer server. The refusal then names that reason instead of `unknown-build`, which would
  wrongly tell the user to update an app that is already newer than the server.
- Only the handshaking device's row is written. Another device in the same role and distribution class
  gains nothing from it.
- A device enrolled before migration 0112, or one that enrolled without a Client Contract, obtains its
  mapping the same way on its first handshake with a verified build.

## Changed assertions

Before this change, two existing tests asserted refusals for a device that had no mapping:

- `TestShippedNativeBuildEnrollsAndPassesTheHandshake`: a sibling device that enrolled with no Client Contract, and one that enrolled with a tampered digest, were both refused at handshake with a genuine shipped build.
- `TestCompatHandshakeRefusesUnverifiedBuildProvenance` (the case "same enrollment classes without an exact build mapping"): a second session on a device with no mapping was refused.

Each of those devices now gets its own verified mapping on its first handshake with a genuine shipped build, as the ticket requires for pre-0112 devices. The tests now assert the ticket's meaning of "a sibling device gains nothing": a device's rebind never adds or changes another device's rows. Separate tests cover a tampered digest, a build below the rollback floor, a wrong pinned surface, and a wrong role or distribution. Each of those records nothing and is refused.

## Consequence

Once a native device session is entitled to a build, the exact enrollment mapping protects nothing beyond what the catalog checks already protect: role, distribution, pinned device surface, rollback floor and artifact digest. A device whose persisted surface is still `core-web-ui`, including one enrolled through the web routes whose credential is replayed as native headers, can pin itself to a native surface with a verified shipped build. It gains nothing it couldn't get by redeeming a pairing code at `POST /api/enroll` with that build's contract.

## Non-goals

- Changing enrollment-time binding, the build catalog format, or the rollback floor.
- Letting a web-cookie or API-token credential create a native build mapping.
