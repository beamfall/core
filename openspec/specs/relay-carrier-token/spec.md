# relay-carrier-token Specification

## Purpose
How the server authenticates its relay carrier connection: minting and rotating the v2 carrier token, and keeping relay secrets out of diagnostics.

## Requirements

### Requirement: Relay carrier dials present a freshly minted v2 token

The relay carrier SHALL authenticate every dial with a "Carrier token v2" minted in memory from
the shared secret in `<DataDir>/secrets/relay-token`, over the hostnames sent in the hello, with a
fresh 16-byte random nonce and a signed lifetime of at most five minutes. The carrier SHALL NOT
send the shared secret, store a minted token, or re-send a token.

#### Scenario: Published vectors reproduce

- **WHEN** the minter runs on each vendored vector's fixed key, hostnames, times and nonce
- **THEN** it produces the vector's MAC input, MAC and token text exactly

#### Scenario: Consecutive dials

- **WHEN** the carrier dials twice, including after an `unauthorized` rejection
- **THEN** the two hellos carry different tokens with different nonces, and neither token
  contains the shared secret

#### Scenario: Insecure secret file

- **WHEN** the relay-token file is group- or other-accessible
- **THEN** the dial is refused with a reason that names neither the secret nor its path

### Requirement: Relay carrier rotates before token expiry

The carrier loop SHALL register a same-identity replacement carrier with a freshly minted token
2 minutes after each registration while the current session keeps carrying. A superseded
session ending SHALL NOT trigger a redial and SHALL NOT change the Console relay state.

#### Scenario: Rotation while the first session drains

- **WHEN** 2 minutes pass after a registration
- **THEN** a second registration happens before the first session ends, and the first session's
  end causes no redial and no relay state change

### Requirement: Relay secrets stay out of diagnostics

Errors, logs and the Console relay state SHALL NOT contain the shared secret or any minted token,
in raw or base64 form.

#### Scenario: Relay echoes credentials

- **WHEN** the relay rejects a carrier with an error that echoes the secret or the minted token
- **THEN** the returned error and the relay state carry a redaction marker instead
