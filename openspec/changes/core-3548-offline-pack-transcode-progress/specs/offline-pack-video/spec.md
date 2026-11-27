## ADDED Requirements

### Requirement: Live video transcode progress and speed telemetry

While a background offline-pack build is transcoding a plaintext video item, the server SHALL parse FFmpeg `-progress` (`out_time_us` and `speed`) and update the build's monotonic `progress` below `1`. `OfflinePackInfo` SHALL expose optional `transcodeSpeed` as the live encoding speed multiplier during active transcoding, and omit or zero it when not transcoding.

#### Scenario: Active video transcode reports progress and speed

- **WHEN** a video offline-pack build runs and FFmpeg emits `out_time_us` and `speed`
- **THEN** `offlinePacks` reports the build with `state` `building`, monotonic `progress` between `0` and `1`, and `transcodeSpeed` matching the active multiplier

#### Scenario: Completed or failed build clears transcodeSpeed

- **WHEN** the video transcode finishes, fails, or is cancelled
- **THEN** `OfflinePackInfo` no longer reports a live `transcodeSpeed`
