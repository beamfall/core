# Audio-copy HLS follows the source's real keyframes

Ticket: CORE-0021 (`queue:core:main`, HLS-AUDIOCOPY-KEYFRAME-TIMELINE-1). ADR-0010.

## Why

The audio-copy rung copies the source video (`-c:v copy`) and transcodes only the audio. Its
variant playlist declared a uniform 10s cadence, and a restart at segment N ran ffmpeg with
`-ss N*10 -output_ts_offset N*10`. With stream copy, ffmpeg can only start at a keyframe, so it
started at the last keyframe at or before `N*10` while stamping it `N*10`. After any seek the
delivered audio and video were up to one source GOP earlier than the timeline said, and when the
source GOP was not exactly 10s the real segment boundaries drifted from the playlist.

## What Changes

- The server probes the source's video keyframe times once per file generation (path, size and
  modification time) and keeps them in the catalog (`playback_keyframes`, catalog migration 0114).
  Later sessions, and later server runs, reuse the stored index.
- For the audio-copy rung the variant playlist has one segment per source GOP: one `#EXTINF` per
  keyframe interval with its real duration, and the last segment ends at the file duration.
- A restart at segment N seeks to that segment's exact keyframe time and stamps the output with the
  same time, so segment N's first video sample is presented exactly where the playlist says
  segment N starts. ffmpeg is told to cut at every keyframe, so each segment is one GOP whatever
  the restart point.
- Re-encode rungs keep the forced-keyframe 4s cadence. A transcoder that cannot probe keyframes
  keeps the previous uniform audio-copy cadence.
