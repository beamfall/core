# Live composed picture

Ticket: CORE-1657 (`queue:core:main`).

## Why

An audio Media whose plugin describes a picture (a graphics sidecar or a timed-text document,
ADR-0057 composed renditions) shows that picture only once its precomputed composed rendition is
Ready. Until the Schedule sweep has rendered it, a video-capable client plays the bare audio, so
the first play of a newly added song never shows its picture.

## What Changes

- **Server, playback negotiation**: when a video-capable client asks for such a Media, no Ready
  composed rendition matches the current picture, and the transcode policy and capacity admit a
  stream, the decision is a live composed HLS stream: verdict `transcode`, reason
  `composed-picture`. A Ready matching rendition is still preferred, and a client without H.264
  video still gets the audio, as today.
- **Server, stream grant**: the signed grant carries `composedPicture` (optional, omitted when
  false), so the byte layer composes the picture without re-deciding.
- **Server, HLS sessions**: a composed grant creates a session with one 720p H.264 rung whose
  encoder draws the picture (graphics track or timed text over black) under the audio, from the
  requested segment, through the same admission cap, priorities, idle reap and cleanup as every
  live transcode. Precompute and live share one FFmpeg composition recipe.
- **Server, precompute**: after a live composed play the Media's composed rendition is queued at
  raised priority, so the next play is precomputed.
- **Stream grant**: the signed grant gains `composedPicture` (optional, additive). Clients treat
  the Stream Token as opaque; the grant shape is published in the wire contract's `streamToken`
  section, so the committed wire goldens are regenerated.

## Impact

- Code: `internal/playback`, `internal/ffmpeg`, `internal/streamtoken`, `internal/mediahttp`,
  `internal/store` (raise a queued job's priority), `internal/app`.
- Wire contract: additive optional `streamToken.composedPicture`. Clients need no change to
  benefit.
