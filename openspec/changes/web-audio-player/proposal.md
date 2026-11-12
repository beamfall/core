# Web audio player

Ticket: CORE-1143 (`queue:core:main`).
Branch: `player-audio`, stacked on `player-overlay` (CORE-1141).

## Why

Playing a music track in the web app opens the video player on a black screen: no artwork, title,
artist, album, queue or visualizer, and the route label says "Server transcode" for an mp3 the
browser can play natively. A sweep of the same player also found the "This show" skip-interval
row on films, and "Playback is unavailable" shown with no reason.

Two separate faults cause the transcode:

- The server's direct-play check requires a video codec match whenever the client declares
  `videoCodecs`, so an audio-class item (no video stream) can never direct-play for a client that
  declares a video envelope (the web, Apple, Android).
- The web capability profile declares only `mp4`/`webm` containers and no `mp3` codec, so even a
  fixed server would transcode an mp3.

## What Changes

- **Server, playback negotiation**: an audio-class Media (no video stream) passes the video
  envelope; the container, audio-codec and subtitle checks still decide. This changes behaviour
  for every client that declares `videoCodecs`: audio items they can play now direct-play instead
  of transcoding.
- **Server, `media(id)` read**: `MediaInfo` gains `artworkSlots` (the same poster/backdrop/thumb map
  as `SourceItem`) and `credits`, the Entities the Media's plugin relates to it (each with the
  Entity id, title, kind, the kind's display label and archetype), in the plugin's declaration
  order. Core stays domain-agnostic: it reports the plugin's declared relations and labels, never
  "artist" or "album" by name. Both fields are additive and optional.
- **Web capability profile**: probes audio-only containers (mp3, m4a, flac, ogg, wav) and the mp3
  codec through `canPlayType`, so natively playable audio is declared.
- **Web audio surface**: when `mediaClass` is `audio` the player shows the artwork (or the accent
  fallback), the title and the credits line instead of a black video frame. The transport, Up Next
  and Media Session reuse the existing controls; Media Session also gets the artwork and credits.
- **Web queue**: a card played from a list (Section, See all, search, Entity detail) carries the
  list's playable siblings into the player. The player shows previous/next controls, declares the
  next item to the server (`nextMediaID`) so Up Next and gapless auto-advance work, and carries the
  queue forward on advance.
- **Route label**: "Server transcode" appears only when the server actually transcodes; a direct
  audio play says "Direct play".
- **Per-container skip interval**: the skip-interval override row shows only when the Media sits in
  a container Entity (archetype `container`), is labelled with that container's kind ("This
  album", "This show") and keys the preference on the container, not the single item.
- **Unavailable reason**: when the server refuses playback, the message names the server's reason
  in plain words instead of a bare "Playback is unavailable".

## Not in this change

- **Visualizer**: the acceptance criterion is a visualizer from the pinned Oscillux release. No
  Oscillux release is pinned yet (OSC-0020 is owner-gated), and its web package carries DSP only, no
  renderer or shader bundle. Per ADR-0228 no effect code is written in the client; the visualizer
  lands when Oscillux publishes a pinned web renderer, and CORE-1143 stays open until then.

## Impact

- `internal/playback` (direct-play rule), `internal/graph` (`MediaInfo`), `internal/web/app`
  (viewer player, capability profile, cards, strings).
- No migration, no store change. Wire changes are additive.
