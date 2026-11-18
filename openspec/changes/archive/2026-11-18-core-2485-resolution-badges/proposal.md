# Resolution and HDR badges

Ticket: CORE-2485 (`queue:core:main`). Native follow-ups: CORE-2486 (Apple), CORE-2487 (Android).

## Why

A viewer cannot tell whether a title is 4K, 1080p or 720p, or whether it is HDR, before or during
playback. Plex and Infuse badge this on the detail page and in the player's info. Core already
stores the frame size of the file a Media row was probed from and each file's HDR signalling, but
no client is told a resolution, and the player's info shows only the rendition height.

## What changes

- Core derives one resolution label from a frame size: `8K`, `4K`, `1080p`, `720p` or `SD`. Either
  dimension reaching a class is enough, so a scope film keeps its class (3840x1600 is 4K). Clients
  show the label as given instead of each computing their own.
- `mediaTracks` and `playbackInfo` Versions carry `resolution` for the Version whose frame size is
  stored; another Version's frame size is unknown and its label is omitted.
- `playbackInfo` gains `sourceVideo { resolution width height hdr }`, the negotiated file's video
  before any transcode, and each advertised rendition carries its own `resolution`.
- Audio-class items are never labelled: their video stream is cover art.
- The web Media detail page shows a resolution badge and an HDR badge (HDR10, HDR10+, HLG, Dolby
  Vision) beside the Version facts. The player's info overlay shows the source's resolution and
  HDR and, while transcoding, the rendition being played.
- The client-core wire contract gets an additive minor version (2.27.0).

## Non-goals

- Badges on poster cards (Plex shows none there).
- Storing a frame size per file; Versions other than the probed one stay unlabelled.
- Native clients (CORE-2486, CORE-2487).

## Failure modes

- An unknown frame size yields no label and no badge, never a guess.
- A failed `mediaTracks` read hides the detail badges as it hides the pickers.
