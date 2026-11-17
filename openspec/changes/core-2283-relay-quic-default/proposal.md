# Relay carrier: QUIC by default, TLS/TCP as the fallback

Ticket: CORE-2283 (`queue:core:main`).

## Why

The relay carrier defaults to TLS/TCP. With the unsent-backlog bound (CORE-2282) a small control
stream (playback sync, clock exchange) stays inside the relay's 250 ms budget on a clean link, but
through real 1% packet loss it measures 330 to 353 ms: one lost TCP segment holds every yamux
stream on the carrier until it is retransmitted. The QUIC carrier recovers loss per stream and
measured 66 ms in the same run (beamfall-relay `docs/relay-latency-budget.md`). A lossy uplink is
the case that matters for screens kept in sync with the audio.

## Decision

Recorded in beamfall-relay's change `core-2283-quic-default-carrier`: QUIC becomes the default
carrier, TLS/TCP the fallback; a second carrier for control streams is not taken.

## What Changes

- `remote_access.relay.transport` (flag `--relay-transport`, `BEAMFALL_RELAY_TRANSPORT`) gains
  `auto`, the new default. `tcp` and `quic` stay as explicit choices and never fall back.
- With `auto`, `RelayClient.Connect` tries QUIC at the relay endpoint (same host and port, UDP)
  for at most 3 seconds, covering the dial and the registration. If that fails without the relay
  answering, it connects over TLS/TCP with a newly minted token (the relay refuses a replayed
  nonce) and skips QUIC for 10 minutes. A refusal the relay sends over QUIC is returned as it is.
- The carrier loop's "relay carrier connected" log line names the transport in use.
- The latency harness driver (`TestCarryForRelayLatencyHarness`) takes the transport from
  `CARRIER_TRANSPORT`, defaulting to the default configuration.

## Not in this change

- No protocol change. An older relay without a QUIC listener costs at most 3 seconds once per 10
  minutes and then carries over TLS/TCP as before.
