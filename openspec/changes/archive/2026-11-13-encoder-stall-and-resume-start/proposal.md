# Restart a stalled encoder, and start a resumed transcode once

Tickets: CORE-1344 (`queue:core:main`, PLAYBACK-ENCODER-STALL-WATCHDOG-1) and CORE-1345
(PLAYBACK-RESUME-DOUBLE-SPAWN-1).

## Why

A live HLS session trusts any encoder in its window. An encoder that hangs without exiting,
such as a deadlocked hardware encoder or a source on a stalled network mount, is never detected.
Each client retry refreshes the session's last access, so the idle reap never fires, and the
client loops on 503s indefinitely.

Resuming a transcode at an offset spawns the encoder twice. The `init.mp4` request starts the
encoder at segment 0. The first media request, at the resume segment, falls outside that
encoder's window, so it kills the encoder and starts another one. That costs about 0.4 to 0.9 s
of extra time to first frame on every resume. The resume position is already computed when
playback info is decided, but it never reaches the session.

## What Changes

- Every live encoder reports its progress on a pipe (`-progress pipe:1`). The process keeps the
  time its output position last advanced.
- While a request waits on an encoder, a watchdog kills the encoder if its output position has
  not advanced for a bounded interval. The kill counts as a failed run, so the waiting request
  restarts the encoder within the existing restart bound and session budget. Once the bound is
  used up, the request fails with the encoder-failing error. An encoder nobody is waiting on is
  left alone.
- The playback-info decision records the viewer's resume position for the session it addresses.
  The session starts its first encoder at the segment that contains that position.
- An `init.mp4` request reuses any encoder already running for its rendition, because every
  encoder writes the init segment when it starts. With no encoder running, it starts one at the
  resume segment.

The resume position is held server-side, keyed by the same session identity as the stream
token (session, media and capability fingerprint). Adding it to the token would need a wire
contract bump and consumer repins. A master-URL parameter would be dropped by the relative
variant URIs. The server-side hint expires with the idle-session timeout.
