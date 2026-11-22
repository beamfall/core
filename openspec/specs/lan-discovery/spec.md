# lan-discovery Specification

## Purpose
How the server advertises itself on the local network, including the TXT record fields clients read before they connect.

## Requirements

### Requirement: Discovery TXT record carries scheme and a stable server id

The server's `_beamfall._tcp` mDNS TXT record SHALL carry `version`, `path=/`, `scheme` and `id`.
`scheme` SHALL be what the advertised port answers to a LAN client. `id` SHALL be stable for the
server's identity and SHALL NOT be a secret or key material. The record SHALL NOT carry setup or
claim state.

#### Scenario: Default install advertises http

- **WHEN** the server starts with discovery on and direct TLS off
- **THEN** the TXT record contains `scheme=http`

#### Scenario: Direct TLS advertises https

- **WHEN** the server starts with discovery on and direct TLS active
- **THEN** the TXT record contains `scheme=https`

#### Scenario: The id survives a restart

- **WHEN** the server restarts with the same data directory
- **THEN** the TXT record's `id` is unchanged
- **AND** it is 32 lowercase hex characters derived one-way from the public key pin

#### Scenario: Setup state is never advertised

- **WHEN** the server has not been set up
- **THEN** the TXT record contains no setup or claim entry

### Requirement: A loopback-only listener is not advertised

The server SHALL NOT advertise `_beamfall._tcp` when its listener is bound to a loopback address
only, because no other device can connect to that port. It SHALL advertise when the listener is
bound to a wildcard or non-loopback address and discovery is on.

#### Scenario: Default loopback listener stays off the LAN

- **WHEN** the server starts with discovery on and listens on `127.0.0.1:2326`
- **THEN** no `_beamfall._tcp` record is registered
- **AND** the server logs that discovery was not advertised

#### Scenario: Wildcard listener advertises

- **WHEN** the server starts with discovery on and listens on `0.0.0.0:2326`
- **THEN** the `_beamfall._tcp` record is registered on port 2326
