# Remux instead of transcode when only the container is undeclared

Ticket: CORE-1145 (`queue:core:main`). Restores ADR-0010 §2 (direct-stream is a remux).

## Why

A local mkv whose H.264 video and AAC audio the browser decodes was reported as "Server transcode ·
Auto". The label was truthful: the server really re-encoded. `audioCopyEligible` required the
source container to be in the profile's `containers`, and the web client declares only `mp4` and
`webm`, so any mkv skipped both direct play and the video-copy rung and fell to the full transcode.
ADR-0010 §2 says a container-only mismatch is a remux (direct-stream), and that remux must keep
HDR metadata.

An mp3 hit the same wall twice. The web profile declared no audio containers or audio codecs, and
the video-codec gate looked the item's audio codec up among the video codecs, so no audio item
could ever direct-play against a profile with a `videoCodecs` envelope.

## What Changes

- Playback negotiation treats an undeclared source container with playable streams as the remux
  case: decision `direct-stream`, reason `container-remux`, one video-copy HLS rung. The
  HDR-preservation rule gates it: a source with no recorded video stream, or with HDR10+ or Dolby
  Vision metadata, still takes the full transcode (or the existing refusal).
- For an audio-class item the video-codec gate no longer applies when the profile declares
  `videoCodecs`; its audio streams are judged against `audioCodecs`.
- The web client's probed profile declares audio file containers (`mp3`, `flac`, `ogg`, `m4a`,
  `wav`) and the `mp3` and `vorbis` audio codecs it can play from a file.

## Wire compatibility

No wire change: `decision` and `reason` keep their vocabularies (`container-remux` already exists).
Clients see `direct-stream` where they previously saw `transcode` for these sources.

## Non-goals

- Copying compatible audio during a remux. The rung still uses the profile's HLS audio target, so
  `container-remux` can re-encode AAC to AAC; copy-through is a follow-up.
- Capability profile changes for other clients.
