# Live FFmpeg progress and transcode speed for offline pack builds

Ticket: CORE-3548 (`queue:core:main`).

## Why

Background offline-pack video builds currently report coarse step-level progress (`0` until the whole video finishes transcoding) and no encoding speed metric. While a large video transcodes, clients have no live percentage or speed telemetry (such as `Transcoding (70%) ~ 14.1x`).

## What Changes

- `internal/ffmpeg/convert.go` adds `Runner.ConvertWithProgress` (and `ConvertProgress`) using FFmpeg `-progress pipe:1` to stream monotonic `out_time_us` and `speed` multiplier samples while keeping `Runner.Convert` behavior unchanged.
- `internal/app/offline_pack_transcode.go` forwards `ConvertProgress` (and probed source duration when available) from `offlinePackTranscoder` into `offlinepack.Transcoder`.
- `internal/offlinepack` (`build_state.go`, `service_state.go`, `offlinepack.go`) updates building pack progress smoothly during video transcodes (weighted and monotonic, throttled to the store, staying `< 1` until sealed) and tracks live in-memory `transcodeSpeed` while actively transcoding.
- `OfflinePackInfo` (`internal/graph/graph_offline_packs.go`) exposes optional `transcodeSpeed` alongside `progress` on `offlinePacks` for active builds.

## Non-goals

- No SQLite schema migration: `transcodeSpeed` is ephemeral live telemetry for active in-process transcodes.
- Encrypted-library pipe transcodes (`TranscodeToPipe`) keep fragmented MP4 on `pipe:1` and continue reporting step-level progress.
- Client UI sheets and telemetry formatting live in sibling Apple (`CORE-3549`) and Android (`CORE-3550`) tickets.
