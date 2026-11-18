# Version, audio and subtitle pickers on the web Media detail page

Ticket: CORE-2442 (`queue:core:main`).

## Why

Plex shows a title's video version, audio track and subtitles on its detail page, each with a
picker, before Play. Beamfall has that data only in `/api/playback-info`, which negotiates a stream
and mints stream tokens, so the web Media detail page cannot show the tracks or let the viewer pick
one until playback has started.

## What changes

- A new session-role graph read, `mediaTracks(mediaID)`, lists a Media item's Versions and, for
  each Version, the audio and caption tracks of its primary Part. It reads the stored stream and
  sidecar inventory only: no negotiation, no stream token, no playback session. Version ids are the
  ids `playbackInfo` reports, and caption ids are the ids `playbackInfo` gives the same tracks, so a
  pick made from the listing names the same track in playback.
- The web Media detail page shows Version, Audio and Subtitles pickers under the action row when the
  item has more than one choice for them. The Version pick is remembered the way the player
  remembers it (CORE-1676). The audio and subtitle picks ride to the player in navigation state and
  reach `playbackInfo` as `selectedAudioIndex` and, for a subtitle that is burned in,
  `selectedSubtitleIndex`; a text subtitle pick is shown when the stream attaches.
- The client-core wire contract gets an additive minor version for the new field.

## Non-goals

- Native clients (follow-up per client).
- Capability filtering in the listing: it lists what is stored. The player still decides what a
  client can play and drops a pick it cannot honour.

## Failure modes

- A failed `mediaTracks` read hides the pickers; Play behaves as before.
- A Media the session cannot see answers `unknown media`, like `mediaFiles`.
- A pick that no longer matches a track when playback starts is ignored and the server's choice
  applies.
