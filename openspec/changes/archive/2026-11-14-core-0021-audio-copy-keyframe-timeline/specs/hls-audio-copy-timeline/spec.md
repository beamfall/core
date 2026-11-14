## ADDED Requirements

### Requirement: The audio-copy playlist follows the source's keyframes

For an HLS session whose rung copies the source video, the server SHALL build the variant
playlist from the source's probed video keyframe times: one segment per keyframe interval, each
declaring that interval's duration, with the last segment ending at the source duration.

#### Scenario: Source with a 7s GOP

- **WHEN** an audio-copy session starts for a source whose keyframes are 7s apart
- **THEN** each declared segment duration equals the real keyframe interval and the durations sum
  to the source duration within one frame

### Requirement: An audio-copy restart is stamped at its real keyframe time

When the encoder for a copy-video rung starts or restarts at segment N, the server SHALL seek to
segment N's keyframe time and offset the output timestamps by that same time.

#### Scenario: Restart mid-file

- **WHEN** a client requests segment N of an audio-copy session and the encoder restarts there
- **THEN** the first video sample of segment N is presented at the playlist's cumulative start
  time for segment N

### Requirement: The keyframe index is probed once per file generation

The server SHALL probe a source's keyframe index at most once per file generation, identified by
path, size and modification time, and SHALL reuse the stored index for later sessions.

#### Scenario: Second session for the same file

- **WHEN** a second audio-copy session starts for an unchanged file
- **THEN** the stored keyframe index is used and the source is not probed again
