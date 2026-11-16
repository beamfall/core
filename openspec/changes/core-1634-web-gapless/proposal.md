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
- Decoded audio is bounded: a next track whose metadata declares more than 12 minutes (or no
  duration) is not decoded, each decoded track is released when it finishes, and at most two are
  held (the one heard and the next).
- A focused Playwright test measures the silence between test tones at each join.

## Non-goals

- Browsers that play HLS through their own engine (Safari), even for direct-play files: the player
  reuses one media element across items and a media element source cannot be removed, so routing
  it for a direct-play track leaves a later native-HLS item (a transcoded next item, or a switch to
  HLS mid-track) silent. CORE-1671 hit exactly this and keyed routing to the browser's HLS engine
  (commit 9f637c65); this change keeps that rule, so Safari keeps the element advance and its gap.
  Joining there needs a design that never routes the element (task 8).
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
