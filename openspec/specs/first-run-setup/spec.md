# first-run-setup Specification

## Purpose
First-run setup of a new server: atomic commit of the owner and settings, the setup token lifetime, and recovery after an interrupted setup.

## Requirements

### Requirement: First-run setup commits atomically

A successful first-run setup SHALL commit the owner Profile, its owner designation, the first
Manager Device and that Device's Session in one store transaction. A failure at any of those steps
SHALL leave none of them stored.

#### Scenario: Enrollment fails after the Profile is written

- **WHEN** the first Manager Device or its Session cannot be written during first-run setup
- **THEN** no new Profile, owner designation, Device or Session is stored, and the setup token
  stays valid for a retry in the same process

### Requirement: Setup token is issued until setup completes

At startup the server SHALL install a first-run setup token unless first-run setup is complete. Setup
SHALL count as complete only when an adult Profile exists and at least one Device has been
enrolled. Once complete, the server SHALL NOT install a setup token on any later start.

#### Scenario: Restart after a partial owner write

- **WHEN** the store holds an adult Profile but no Device, because an earlier setup stopped after
  writing the Profile, and the server starts
- **THEN** a setup token is installed and first-run setup can be completed through the normal setup
  route without editing the database

#### Scenario: Restart after successful setup

- **WHEN** first-run setup has succeeded and the server starts again
- **THEN** no setup token is installed and setup requests are refused

### Requirement: Setup recovery reuses the partial owner

When first-run setup completes on a store that holds Profiles but no Device, the server SHALL reuse
the designated owner Profile, or the oldest adult Profile when none is designated, as the owner
instead of creating another Profile. The store SHALL end with exactly one owner Profile, and the new
Manager Device's Session SHALL be bound to it.

#### Scenario: Setup after a partial owner write

- **WHEN** setup completes on a store holding an owner Profile and no Device
- **THEN** the store holds the same number of Profiles as before, that Profile is the only owner,
  and exactly one Device, with the Manager role, is enrolled for it

### Requirement: A local-network request may complete setup without the token

While first-run setup is pending, the server SHALL accept a setup request with no setup token when
the request arrived on the local network (the server's LAN test: loopback, private or link-local
source, not relayed, not forwarded except through a declared proxy) and passes the host rule below.
Any other setup request SHALL
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

### Requirement: A tokenless setup claim names a host that cannot be rebound

A setup request without a token SHALL be accepted only when the host the client addressed is an IP
literal, `localhost` or an mDNS `.local` name, so a public website cannot use DNS rebinding to claim
the server through a local browser.

#### Scenario: Local-network request addressed by a public host name

- **WHEN** setup is pending and a request from a private address whose Host is a public DNS name
  submits setup with no token
- **THEN** setup is refused and nothing is created, so a DNS-rebinding page cannot claim the server

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

### Requirement: Provider keys offered during setup

The first-run setup Plugins step SHALL list each provider key that an enabled plugin's scrapers
need and that is not yet configured, with an optional field for the owner to enter it.

#### Scenario: Owner enters a key

- **WHEN** the owner types a key for a listed provider and continues past the Plugins step
- **THEN** the wizard saves it with `setScraperCredential` for that AuthRef after activating plugins

#### Scenario: Owner skips the key

- **WHEN** the owner leaves every key field blank and continues
- **THEN** setup proceeds to the Library step without saving any credential

#### Scenario: Plugin disabled

- **WHEN** the owner switches off a plugin
- **THEN** the provider keys only that plugin needs are no longer listed
