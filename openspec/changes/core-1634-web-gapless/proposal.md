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
- The decoded tracks play on an AudioContext of their own; the media element is never routed through
  Web Audio. A media element source cannot be removed and the player reuses one element across
  items, so routing it would silence a later item Web Audio cannot hear (native HLS, the CORE-1671
  regression fixed in commit 9f637c65). One path therefore serves Chrome, Safari and Firefox,
  whatever stream the current item plays. A silent source keeps the context's output running, since
  an engine can idle it while nothing plays and lose the start of the first decoded track.
- The media element keeps driving the transport, progress, Media Session and the advance: from the
  end of its track it plays the next track muted, moved back within a quarter second of what is
  heard at most once a second (an engine can report its old position for a while after a seek). The
  listener's own mute is kept apart while the element is held muted, so the transport and remote
  control show and change the listener's choice. While the
  decoded audio plays, the following track is chained on the same clock, so later joins are
  sample-accurate.
- A pause, seek, rate change, an advance to anything other than the decoded track, an audio delay,
  anything else unmuting the element, or leaving the player hands sound back to the media element
  at its own position.
- Decoded audio is bounded: a next track whose metadata declares more than 12 minutes (or no
  duration) is not decoded, each decoded track is released when it finishes, and at most two are
  held (the one heard and the next).
- A focused Playwright test measures the silence between test tones at each join.

## Non-goals

- HLS transcodes as the next track (they are segmented, not one decodable file), a next track whose
  stream is on another origin, video, composed pictures and karaoke songs.
- An audio delay above zero: the gapless join stays off while one is set, and the audio delay keeps
  its own element routing (CORE-1671), which the join never uses.
- Keeping the decoded audio across a pause or seek; the next join after one again starts from the
  element.
- Visual effects: this change schedules audio only.

## Capabilities

### New Capabilities

- `web-gapless-playback`: the web player's preloaded, trimmed, clock-scheduled track joins.

### Modified Capabilities

None.
