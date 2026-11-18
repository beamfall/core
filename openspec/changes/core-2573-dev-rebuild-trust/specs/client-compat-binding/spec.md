## ADDED Requirements

### Requirement: A rebuilt dev client keeps the device it was admitted on

When an enrollment already holds the exact mapping to a build whose record came from the operator's
runtime dev-build file, build provenance SHALL accept a well-formed artifact digest that differs from
the record's digest, with every other provenance check unchanged. Creating a mapping SHALL still
require the record's exact digest, and a release record SHALL always require its exact digest.

#### Scenario: Owner rebuilds a debug client

- **WHEN** a device that verified an operator-admitted `-dev` build handshakes with the same build id
  and a new artifact digest
- **THEN** the handshake is servable without editing the dev-build file or restarting the server

#### Scenario: Device without the mapping

- **WHEN** a device with no mapping to that `-dev` build enrolls or handshakes with a digest the file
  does not name
- **THEN** no mapping is recorded and the handshake is refused with `client-too-old` / `unknown-build`

#### Scenario: Release record

- **WHEN** a device mapped to a release build presents that build id with another digest
- **THEN** the handshake is refused with `client-too-old` / `unknown-build`

#### Scenario: Record removed

- **WHEN** the operator removes the `-dev` record from the dev-build file
- **THEN** the mapped device's next handshake is refused, without a restart
