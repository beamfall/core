# client-compat-binding Specification

## Purpose
How a client session's compatibility decision is bound to its authentication state, and when that decision is re-derived.

## Requirements

### Requirement: Web UI decisions follow the session's authorization changes

The server SHALL re-derive a web UI session's compatibility decision from its embedded UI contract
under the session's current authorization state when the stored decision differs from that state
only in authorization, and SHALL serve the request when the re-derived decision is servable.
Every other credential class SHALL be refused with `client-too-old` / `unknown-build` on an
authorization change until it handshakes again.

#### Scenario: Web session creates a Library

- **WHEN** a web UI session with a servable decision creates a Library and then makes another request
- **THEN** the next request is served without a new handshake

#### Scenario: Re-derived decision keeps its lifetime

- **WHEN** a web UI session's submitted decision is re-derived after its grants change
- **THEN** the new decision stays submitted and keeps the original expiry, so it does not lapse at the provisional TTL

#### Scenario: Native credential's authorization changes

- **WHEN** a native credential's authorization state changes after its handshake
- **THEN** its next compatibility-bound request is refused with `client-too-old` / `unknown-build`

#### Scenario: Other mismatches keep their outcome

- **WHEN** a web UI session's stored decision has expired, or was submitted by the client under an
  earlier policy generation, and its authorization state has also changed
- **THEN** the request is refused as it would be without the authorization change

### Requirement: Refusal survives authorization changes

The server SHALL keep a refused subject refused across an authorization change, and only a
client-submitted servable handshake SHALL clear the refusal.

#### Scenario: Refused web session gains a Library

- **WHEN** a web UI session has been refused and its Profile is then granted a Library
- **THEN** its requests stay refused until it submits a servable handshake

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
- **AND** listing the same build id again, with any digest, restores that device

#### Scenario: Rebuild is audited

- **WHEN** a handshake is admitted through the dev-rebuild exception
- **THEN** the server logs the device, build id and both digest prefixes, and nothing new reaches the wire
