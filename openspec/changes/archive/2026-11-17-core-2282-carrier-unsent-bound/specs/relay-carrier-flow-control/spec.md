## ADDED Requirements

### Requirement: The TCP relay carrier bounds its unsent send backlog

On Linux and macOS the server SHALL hold the TLS/TCP relay carrier socket's unsent send backlog
to 16 KiB, the relay's value, applied to the TCP connection below TLS. The bound SHALL NOT limit
bytes in flight. On other platforms the carrier SHALL connect without the bound. The QUIC carrier
is unaffected.

#### Scenario: Media saturates the carrier on Linux

- **WHEN** the server dials its relay over TLS/TCP on Linux
- **THEN** the carrier socket's `TCP_NOTSENT_LOWAT` is 16 KiB

#### Scenario: The relay stops reading on macOS

- **WHEN** the server writes to a macOS carrier whose peer has stopped reading
- **THEN** the writer is held with no more than about 16 KiB written but unsent, and a write
  deadline still ends the wait

#### Scenario: Another platform

- **WHEN** the server dials its relay on a platform without the control
- **THEN** the carrier connects and carries streams as before
