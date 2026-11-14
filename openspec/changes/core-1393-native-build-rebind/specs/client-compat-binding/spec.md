## ADDED Requirements

### Requirement: Native handshake rebinds a verified newer build

When a native device credential presents a build at handshake that is not mapped to that device
on the presented surface, the server SHALL record it as the device's mapping for that surface if
it passes the same provenance checks as enrollment against the device's server-held enrollment.
The handshake SHALL be evaluated against the mapping as it stands after the rebind.

#### Scenario: Enrolled device upgrades to a newer shipped build

- **WHEN** a device enrolled with a shipped build handshakes with a newer shipped build for the same
  surface, role and distribution class
- **THEN** the handshake is servable without re-pairing and the device's mapping names the newer build

#### Scenario: Device enrolled before the mapping existed

- **WHEN** a device with no build mapping handshakes with a shipped build it is entitled to
- **THEN** the device obtains a mapping for that build and the handshake is servable

### Requirement: Handshake rebind stays within the rollback floor and the calling device

The server SHALL NOT record a handshake build below the surface's rollback floor, and SHALL write only
the handshaking device's mapping. An older verified build at or above the floor MAY replace a newer
mapping.

#### Scenario: Downgrade

- **WHEN** a device handshakes with a build below the surface's rollback floor
- **THEN** its mapping is unchanged and the handshake is refused with `client-too-old` / `unknown-build`

#### Scenario: Rollback above the floor

- **WHEN** a device mapped to a newer build that its server refuses as `server-too-old` handshakes with
  the previous shipped build, still at or above the floor
- **THEN** its mapping moves back to that build and the handshake is servable

#### Scenario: Tampered digest

- **WHEN** a device presents a shipped build id with an artifact digest that does not match the catalog
- **THEN** its mapping is unchanged and the handshake is refused with `client-too-old` / `unknown-build`

#### Scenario: Sibling device

- **WHEN** one device rebinds to a newer build
- **THEN** no other device's mapping is added or changed
