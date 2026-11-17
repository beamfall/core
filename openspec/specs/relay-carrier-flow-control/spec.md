# relay-carrier-flow-control Specification

## Purpose
Keep the relay carrier's yamux flow-control window matched on both ends of the tunnel, so a slow reader on one side backpressures the other instead of stalling or dropping streams.

## Requirements

### Requirement: Relay carrier yamux settings match the relay

The relay carrier SHALL use the relay's default yamux stream window of 4 MiB and a 30-second
`StreamCloseTimeout`, unless the operator sets the window with
`--relay-yamux-stream-window-bytes` or `BEAMFALL_RELAY_YAMUX_STREAM_WINDOW_BYTES`. A test SHALL pin
these values to the relay's.

#### Scenario: Default settings

- **WHEN** the server dials its relay with no window configured
- **THEN** the carrier's yamux stream window is 4 MiB and its stream close timeout is 30 seconds

#### Scenario: Matching a tuned relay

- **WHEN** the operator sets the window to 8 MiB to match a relay started with
  `--yamux-stream-window-bytes 8388608`
- **THEN** the carrier uses an 8 MiB stream window

#### Scenario: Window below the yamux minimum

- **WHEN** the configured window is negative, non-numeric or below 256 KiB
- **THEN** the server refuses to start and names the setting

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
