# Web gapless music playback

Ticket: CORE-1634 (`queue:core:main`).

## Why

The web player plays one track per media element load: when a track ends, the element fires
`ended`, the player advances to the next track's route and the element loads and buffers the next
stream from nothing. Every album track change therefore has an audible gap, and the encoder delay
and padding core now declares (CORE-1626) were never trimmed, so even a perfectly timed join would
keep a short silence.

## What Changes

- Before a music track ends, the web player fetches and decodes the next track's direct-play stream
  (the stream playback-info pre-mints in `next`) into memory, starting 30 s before the end.
- At the end of the current track the decoded next track is scheduled on the Web Audio clock to start
  where the current one stops, after trimming the `streams[].gapless` encoder delay and padding.
  The trim applies only where the declared samples are near silence, so a browser decoder that
  already trimmed them is not trimmed a second time.
- The media element keeps driving the transport, progress, Media Session and the advance: after the
  join it plays the next track silently, kept within a quarter second of what is heard. While the
  decoded audio plays, the following track is chained on the same clock, so later joins are
  sample-accurate.
- A pause, seek, rate change, volume change, an advance to anything other than the decoded track,
  or leaving the player hands sound back to the media element at its own position.
- The element's sound is routed through one shared Web Audio graph per element, used by both the
  audio delay (CORE-1671) and the gapless join, since a media element can feed only one source node.
- A focused Playwright test measures the silence between test tones at each join.

## Non-goals

- Browsers that play HLS through their own engine (Safari): the element is never routed through Web
  Audio there (CORE-1671), so they keep the element advance, with its gap.
- HLS transcodes as the next track (they are segmented, not one decodable file), a next track whose
  stream is on another origin, video, composed pictures and karaoke songs.
- An audio delay above zero: the gapless join stays off while one is set.
- Keeping the decoded audio across a pause or seek; the next join after one again starts from the
  element.
- Visual effects: this change schedules audio only.

## Capabilities

### New Capabilities

- `web-gapless-playback`: the web player's preloaded, trimmed, clock-scheduled track joins.

### Modified Capabilities

None.
