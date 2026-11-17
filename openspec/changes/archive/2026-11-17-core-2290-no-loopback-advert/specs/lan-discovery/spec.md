## ADDED Requirements

### Requirement: A loopback-only listener is not advertised

The server SHALL NOT advertise `_beamfall._tcp` when its listener is bound to a loopback address
only, because no other device can connect to that port. It SHALL advertise when the listener is
bound to a wildcard or non-loopback address and discovery is on.

#### Scenario: Default loopback listener stays off the LAN

- **WHEN** the server starts with discovery on and listens on `127.0.0.1:8800`
- **THEN** no `_beamfall._tcp` record is registered
- **AND** the server logs that discovery was not advertised

#### Scenario: Wildcard listener advertises

- **WHEN** the server starts with discovery on and listens on `0.0.0.0:8800`
- **THEN** the `_beamfall._tcp` record is registered on port 8800
