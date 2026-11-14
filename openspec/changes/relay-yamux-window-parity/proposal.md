# Relay carrier yamux window parity

Ticket: CORE-1386 (`queue:core:main`).

## Why

The server's relay carrier runs yamux with a 1 MiB stream window and the library's 5-minute
`StreamCloseTimeout`. yamux refills a stream's window only after half of it drains, so one relayed
upload is held to about window/2 per round trip: roughly 10 MB/s at 50 ms. The relay is raising its
default window to 4 MiB and already closes abandoned streams after 30 s. The server is the receiver
for uploads and half of every stream close, so it must match.

## What Changes

- The relay carrier's default yamux stream window becomes 4 MiB, the relay's default.
- `--relay-yamux-stream-window-bytes` / `BEAMFALL_RELAY_YAMUX_STREAM_WINDOW_BYTES` sets it to match
  a relay started with `--yamux-stream-window-bytes`. 0 uses the default; values below yamux's
  256 KiB initial window are rejected at startup.
- The carrier's `StreamCloseTimeout` becomes 30 s, the relay's value.
- A parity test pins the server's carrier yamux settings to the relay's pinned values.

## Impact

`internal/config`, `internal/remoteaccess`. No wire or contract change: yamux windows are local
receive budgets, and each side's window governs only the direction it receives.
