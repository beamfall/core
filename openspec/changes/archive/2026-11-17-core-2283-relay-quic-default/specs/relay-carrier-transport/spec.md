## ADDED Requirements

### Requirement: The relay carrier prefers QUIC and falls back to TLS/TCP

With the default relay transport (`auto`) the relay client SHALL first try the QUIC carrier at the
relay endpoint and, when that attempt fails without an answer from the relay within 3 seconds,
SHALL connect over TLS/TCP in the same call with a newly minted token. After a fallback that connected it
SHALL NOT try QUIC again for 10 minutes. A configured `tcp` or `quic` transport SHALL use only that
transport.

#### Scenario: Relay reachable over UDP

- **WHEN** the relay client connects with the default transport and the relay's QUIC carrier answers
- **THEN** the carrier session runs over QUIC and no TCP connection is dialled

#### Scenario: UDP does not reach the relay

- **WHEN** the relay client connects with the default transport and the QUIC dial fails or times out
- **THEN** it registers over TLS/TCP, and the token it presents there differs from the one offered over QUIC

#### Scenario: Reconnect after a fallback

- **WHEN** the relay client connects again within 10 minutes of a fallback
- **THEN** it dials TLS/TCP without trying QUIC first

#### Scenario: Relay refuses the carrier over QUIC

- **WHEN** the relay answers the QUIC registration with a refusal frame or by closing the connection
- **THEN** the connect fails with that answer, TLS/TCP is not tried and QUIC is not held off

#### Scenario: Neither transport connects

- **WHEN** the QUIC attempt gets no answer and the TLS/TCP attempt fails too
- **THEN** the next connect tries QUIC first

#### Scenario: Explicit transport

- **WHEN** the configured transport is `quic` and the QUIC dial fails
- **THEN** the connect fails and TLS/TCP is not tried

### Requirement: The transport in use is visible to the operator

The relay client SHALL report which transport its live carrier uses, and the server SHALL include
it in the log line written when the relay carrier connects.

#### Scenario: Fallback in effect

- **WHEN** the carrier connected over TLS/TCP after a failed QUIC attempt
- **THEN** the connect log line names `tcp` as the transport
