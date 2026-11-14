# Web key and tempo controls for karaoke songs

Ticket: CORE-1659 (`queue:core:main`).

## Why

The server can now play a song in another key or at another speed (CORE-1658), and the web player
shows a karaoke song's composed picture (CORE-1656), but the web player has no way to ask for an
adjustment. A singer has to accept the song as recorded.

## What Changes

- **Web, player**: when GraphQL `serverInfo.playbackAdjustments` advertises the ranges and the
  playing Media is served as a composed karaoke picture (the CORE-1656 test), the player bar shows
  key controls (one semitone down, up, reset) and tempo controls (5% slower, faster, reset),
  clamped to the advertised ranges. Without the capability, or for any other Media, nothing shows.
- **Web, playback request**: a change restarts the stream with `adjustment{pitchShiftSemitones,
  tempo}` at the same song position. The default key and tempo send no adjustment.
- **Web, timeline**: an adjusted stream runs at song time divided by the applied tempo. The
  player keeps its position in song seconds: the restart, the resume point, progress reports and
  the transport's time, duration, scrubber and seeks convert with the tempo the response echoed.
- **Web, honest state**: the bar shows the requested key and tempo ("Key +2", "Tempo 95%"); when
  the response did not echo `adjustment` for a non-default request, it says the change is not
  applied.
- **Accessibility**: every button has a label, the controls are reachable and operable by
  keyboard, and the current key and tempo are announced in a polite live region.

## Impact

- Code: `internal/web/app/src` (player engine, transport, player page, a new controls component,
  the playback-info request, a `serverInfo` read, strings).
- No server or wire contract change: the request, response and `serverInfo` fields shipped in
  wire contract 2.5.0 (CORE-1658).
