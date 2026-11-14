# relay-carrier-flow-control Specification

## Purpose
Keep the core remote-access client's yamux flow control in parity with the relay: a 4 MiB default per-stream receive window, an operator-configurable override bounded to the yamux range, and a 30-second stream close timeout, so relayed streams are not throttled by the default 256 KiB window on high-latency links.

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
