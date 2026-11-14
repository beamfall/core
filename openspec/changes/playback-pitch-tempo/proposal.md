# Playback pitch shift and tempo

Ticket: CORE-1658 (`queue:core:main`).

## Why

A listener singing along often needs a song in another key or at another speed. Today the server
can only play a song as recorded, so a client cannot move the key without changing the speed, or
the speed without changing the key.

## What Changes

- **Server, playbackInfo request**: an optional `adjustment` object with `pitchShiftSemitones`
  (whole semitones, -6..+6) and `tempo` (0.75..1.25). Absent, zero pitch and tempo 1 change
  nothing. A value out of range is a 400.
- **Server, playback negotiation**: for an audio Media, a non-default adjustment forces a live
  transcode: the live composed picture when the Media has one and the client takes H.264 video,
  otherwise an audio-only HLS stream. Direct play, cached renditions and the precomputed composed
  rendition are skipped, since none carries the adjustment. The pitch moves without the tempo and
  the tempo without the pitch. When the Media is not audio, or the transcode policy or capacity
  refuses a stream, the adjustment is not applied and the decision is the ordinary one.
- **Server, playbackInfo response**: `adjustment` echoes the applied values, and is omitted when
  nothing was applied. Stream time is song time divided by `tempo`; `timeline` stays in song time.
- **Server, stream grant**: the signed grant carries `pitchShiftSemitones` and `tempo` (optional),
  so a changed value starts a new stream.
- **Server, HLS sessions**: the adjusted session's playlist lasts the song duration divided by the
  tempo, and output segment N starts at song position N x segment length x tempo, so a seek or a
  restart lands on the same song position. A composed picture is retimed with the audio, so
  graphics and timed text stay in sync.
- **Server, GraphQL `serverInfo`**: `playbackAdjustments` advertises the accepted ranges
  (`pitchShiftSemitonesMin`, `pitchShiftSemitonesMax`, `tempoMin`, `tempoMax`).

## Impact

- Code: `internal/playback`, `internal/ffmpeg`, `internal/streamtoken`, `internal/graph`,
  `tools/wire-contract`.
- Wire contract: additive optional fields (request and response `adjustment`, `streamToken`
  `pitchShiftSemitones` and `tempo`, `serverInfo.playbackAdjustments`); published as a new minor
  version.
