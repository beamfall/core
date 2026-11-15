# Audio and subtitle track choice on every web playback route

Ticket: CORE-1709 (`queue:core:main`).

## Why

The web player built its Audio and Subtitles menus from the tracks the browser or hls.js exposed.
On the HLS routes (audio-copy "direct stream" and transcode) the server packs one audio stream and
no subtitles into the playlist, so a file with several audio and subtitle streams offered no choice
at all. Text subtitles shipped as WebVTT URLs never reached a `<track>` element, and bitmap streams
that need burn-in were silently absent.

## What Changes

- `playbackInfo` gains `audioTracks`: every audio stream of the negotiated file (absolute stream
  index, codec, language, channels, default, commentary, visually impaired, selected).
- The request gains `selectedAudioIndex`. A non-default choice skips direct play and cached
  renditions and is served by the audio-copy or transcode rung; the stream token carries the chosen
  stream and ffmpeg maps it (`-map 0:v:0 -map 0:<index>`). A single-track file leaves the grant
  unbound.
- The web player builds its Audio menu from `audioTracks` and its Subtitles menu from
  `captionTracks` whenever the server reports them, on every route. Labels name language, roles
  (forced, SDH, commentary, described), format, and whether the track is external or burned in.
- Text captions (embedded and sidecar, ASS flattened to text) are attached as `<track>` elements
  and switched client-side. Choosing a bitmap stream renegotiates with `selectedSubtitleIndex` so the
  server burns it in. When the server withholds subtitles it cannot deliver to the browser, the menu
  shows a disabled row naming the reason instead of dropping them silently.
- Choices persist across renegotiation (quality, seek recovery, burn-in) for the same media.

## Non-goals

- A WASM ASS renderer (CORE-1719). ASS is offered as flattened text.
- Burning ASS in with libass.
- Bitmap sidecar files; they stay withheld behind the disabled notice row.

## Capabilities

### New Capabilities

- `stream-track-choice`: the audio and subtitle streams a web viewer can choose on every route.

### Modified Capabilities
