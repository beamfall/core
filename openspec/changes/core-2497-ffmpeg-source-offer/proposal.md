# Ship and serve the FFmpeg corresponding source

Ticket: CORE-2497 (`queue:core:main`).

## Why

The Beamfall Core container image ships its own FFmpeg build and its runtime libraries. The
written offer in `NOTICE` must identify the corresponding source for that exact build and make
it available to anyone running the image, without sign-in.

## What Changes

- Build FFmpeg 7.1.4 with the jellyfin-ffmpeg v7.1.4-3 patch series using
  `tools/ffmpeg/build.sh` and the pinned Debian snapshot. The build is GPL-3.0-or-later
  (`--enable-gpl --enable-version3`), excludes `--enable-nonfree` and libfdk-aac, and uses the
  native `aac` encoder.
- Carry one corresponding-source tar for the image's architecture under
  `/usr/share/beamfall/source/ffmpeg/`, beside its `SHA256SUMS`. The archive contains the locked
  upstream inputs, exact Debian library/runtime sources, build scripts, lock and `PACKAGES.txt`
  described in `NOTICE`.
- Serve the archive publicly at `GET /source/ffmpeg`, with `application/x-tar`, an attachment
  filename and `X-Checksum-SHA256`. The GET registration also supports HEAD; `http.ServeContent`
  supplies file length, modification-time, range and conditional-response handling.
- Return 404 when the directory, checksum entry or named regular file cannot be used. Read the
  digest from the first checksum line; do not recompute it or validate tar contents per request.
  The Dockerfile's `ffmpeg-runtime` stage verifies the checksum when building the image.

## Impact

- New `ffmpeg-source-offer` capability with ADDED requirements: no existing spec under
  `openspec/specs/` covers the `/source` route or `/source/ffmpeg`.
- Implementation evidence: `internal/server/source_ffmpeg.go`,
  `internal/server/source_ffmpeg_test.go`, the route in `internal/server/server.go`,
  `tools/ffmpeg/build.sh`, `Dockerfile` and the written offer in `NOTICE`.
- Container images for linux/amd64 and linux/arm64 carry their own source archive. Releases
  without a bundle return 404 from the source endpoint. No schema or store migration.
- This change records the implementation already present in this checkout.
