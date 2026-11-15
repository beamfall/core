# Profile track language preferences on every web playback route

Ticket: CORE-1680 (`queue:core:main`).

## Why

The web viewer ignored the Profile's audio and subtitle language preferences on direct play, and
mostly on hls.js too. Confirmed causes:

- The player read singular keys (`audioLanguage`, `subtitleLanguage`) that the server never sends;
  playbackInfo carries `trackPreferences.audioLanguages[]`, `subtitleLanguages[]`, `subtitleMode`
  and `preferSDH`.
- The audio preference was applied only to hls.js renditions, and the server never chose an audio
  stream by language, so a direct-played two-language MP4 always played its default stream.
- The server's subtitle pick (`selectedSubtitleTrack`) was never applied by the web player.
- Server language matching compared raw tags, so a probed `eng` stream never matched an `en`
  preference.

## What Changes

- Language matching canonicalizes both sides (ISO 639-2 and BCP 47 alike), server and client.
- With no explicit pick, the server delivers the audio stream in the most preferred language that
  the file has (a main track before commentary or description, the file default first within a
  language). A preferred non-default stream moves direct play onto the audio-copy rung, as an
  explicit pick does (CORE-1709). The subtitle pre-selection resolves against that stream.
- The web player takes up `selectedSubtitleTrack` once per Media, before any viewer pick, on every
  route: a text track is shown, a picture-based one is requested again burned in. Forced-only and
  smart modes therefore apply wherever the server's resolver applies them.
- The hls.js audio-rendition fallback reads `audioLanguages`; live text tracks are switched on only
  in `always` mode and only when the server lists no caption tracks.

## Non-goals

- Re-applying preferences after the viewer switches tracks.
- Changing the server's subtitle resolver rules.

## Capabilities

### New Capabilities

- `track-language-preferences`: how a Profile's track language preferences pick the starting audio
  and subtitle tracks on every web route.

### Modified Capabilities
