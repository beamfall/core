# Playback self-healing

Tickets: CORE-1342 (anchor), CORE-1343, CORE-1359, CORE-1360, CORE-1361, CORE-1362, CORE-1363,
CORE-1146 (`queue:core:main`). Branch: `playback-self-healing`.

## Why

A playback and transcode audit on 2026-11-12 found that playback stops for avoidable reasons and
often gives no explanation:

- A live-HLS encoder that exits goes unnoticed. In the test, the next segment request waited out
  its 6 s budget and returned an untyped 503, and the stream only recovered because the client
  retried.
- A truncated source produced 74 empty, 0-duration segments. All of them were served as 200, and
  the player stalled at 2.7 s with no message.
- The web player treats transient faults as "session ended": a token refresh that failed on the
  network, a 410 from an idle reap, or two 401s arriving together. In each case it shows that
  message with only a Back button.
- Safari's native HLS route and direct play never recover from network errors.
- A 20 s connectivity drop is fatal.
- Dragging the scrubber seeks on every pointer move, which restarts the server encoder each time.
- Nothing shows between attach and the first frame, or during a rebuffer.
- The server's unavailable reasons are reduced to "Playback is unavailable right now."

## What Changes

- **Server, live HLS sessions:**
  - A segment request that is waiting notices when the session's encoder exits. It then restarts
    the encoder at the requested segment within the same request, up to a fixed limit. Once that
    limit is used up, it fails fast instead of waiting out the budget.
  - When the encoder exits, the server logs its status and a bounded stderr tail.
- **Server, truncated sources:** a segment with no media, or one past the point where the source
  ended, is never served as playable. The request fails fast, and the server logs where the source
  ended.
- **Web player:**
  - Only an explicit revocation ends a session. Transient faults are retried, or shown as
    retryable.
  - Concurrent 401s share a single token refresh.
  - The native route reloads at the playhead after a network error.
  - The player waits while the browser is offline and resumes when it comes back online.
  - The scrubber seeks once, on release.
  - A spinner covers startup and rebuffering.
  - Every server unavailable reason maps to its own copy and action.

No wire-contract change. The cross-host reason enum, outcome header and renegotiation request
follow in CORE-1353, under the consumer-first contract rollout.
