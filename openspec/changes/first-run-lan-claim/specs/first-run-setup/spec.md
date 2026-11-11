## ADDED Requirements

### Requirement: A local-network request may complete setup without the token

While first-run setup is pending, the server SHALL accept a setup request with no setup token when
the request arrived on the local network (the server's LAN test: loopback, private or link-local
source, not relayed, not forwarded except through a declared proxy). Any other setup request SHALL
present the valid token. A presented token SHALL be checked wherever the request came from, and after
setup completes every setup request SHALL be refused.

#### Scenario: Local-network setup without a token

- **WHEN** setup is pending and a request from a private address submits setup with no token
- **THEN** the owner and the first Manager Device are created and the setup token is consumed

#### Scenario: Remote setup without a token

- **WHEN** setup is pending and a request from a public address submits setup with no token
- **THEN** setup is refused and nothing is created

#### Scenario: Forwarded request through an undeclared proxy

- **WHEN** setup is pending and a request that carries forwarding headers but did not come through
  a declared proxy submits setup with no token
- **THEN** setup is refused and nothing is created

#### Scenario: Wrong token on the local network

- **WHEN** setup is pending and a local-network request submits a wrong token
- **THEN** setup is refused as an invalid token

#### Scenario: Local-network request after setup

- **WHEN** setup has completed and a local-network request submits setup with no token
- **THEN** setup is refused and nothing is created

### Requirement: Setup status reports whether the token is needed

The setup status read SHALL report, alongside whether setup is pending, whether the requesting
client would need the setup token. The web setup wizard SHALL skip its token step when the token is
not needed.

#### Scenario: Wizard opened from the local network

- **WHEN** setup is pending and the wizard is opened from a local-network browser without a token
  in its address
- **THEN** the wizard starts at the admin-password step

### Requirement: The printed setup address is reachable

When the server listens on every interface, its startup output SHALL name the setup address with
the machine's reachable local addresses, not only the loopback address.

#### Scenario: Wildcard bind

- **WHEN** the server listens on `0.0.0.0` and setup is pending
- **THEN** the startup output includes a setup address for each non-loopback local IPv4 address
