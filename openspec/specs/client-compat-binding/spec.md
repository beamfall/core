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
