# Bound the relay carrier's unsent send backlog

Ticket: CORE-2282 (`queue:core:main`). Follows CORE-2262 (beamfall-relay), which bounded the
relay's end of the carrier.

## Why

The TLS/TCP relay carrier multiplexes every relayed stream onto one connection in arrival order.
Bytes the kernel has accepted but not yet sent are a queue that a small stream (playback sync
signals, clock exchanges) cannot overtake. The home server is the end that sends the media, and its
tunnel client leaves that queue to the kernel's send-buffer autotuning, where it reaches megabytes:
on a 20 Mbit/s uplink a sync signal waited 0.6 to 1.0 s behind media. The relay already holds its
own carrier sockets to 16 KiB unsent.

## What Changes

- **Carrier dial:** the TLS/TCP carrier's socket is held to 16 KiB of unsent data, below TLS, on
  Linux (`TCP_NOTSENT_LOWAT`) and macOS (each write waits on `TCP_CONNECTION_INFO`). Bytes in
  flight are not limited, so throughput is unchanged.
- **Windows and other platforms:** no bound. Recorded in the relay's latency documentation, with a
  follow-up ticket for Windows.
- The QUIC carrier is not touched: its streams do not share a send queue.

## Impact

`internal/remoteaccess`. No configuration, wire-contract, schema or migration change.
