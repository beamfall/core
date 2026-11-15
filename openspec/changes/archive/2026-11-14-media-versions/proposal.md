# Media versions: scan grouping and a version picker

Ticket: CORE-1676 (`queue:core:main`).

## Why

A library that holds one title in several files (a 1080p and a 4K encode, a theatrical and an
extended edition) shows one item per file. Playback already negotiates between Versions of one
Media (ADR-0009), but nothing at scan puts two files under one Media, and no client can choose a
Version: the server picks one, and the byte routes always serve the primary file even when
negotiation chose another.

## What Changes

- **Plugin SDK seam**: `ParsedMedia.VersionLabel` (plugin-sdk v0.3.5) lets a filename parser name
  the Version a file is. The film and TV plugin fills it from `Title (Year) - label` names. Core
  reads it without knowing what a label means.
- **Scan grouping**: during a walk, a parsed, non-part, non-image file whose parse matches a file
  already imported from the same directory (same library, class, title, year, sequence and index)
  attaches to that file's Media as another Version, under its parsed label. The first file keeps
  the default slot unless it carries a label of its own. A file whose slot is taken, or whose
  content already belongs to another Media, imports as its own Media, as before. Existing separate
  Media are never merged.
- **playbackInfo request**: optional `selectedVersion`, an opaque version ID taken from a previous
  response. A known ID is played even when it needs a transcode; an unknown ID is ignored and
  negotiation runs as before.
- **playbackInfo response**: `versions`, present only when the Media has two or more Versions, each
  with `id`, `label` (the stored label; empty means the default Version, which clients name in
  their own locale), `videoCodec`, `hdr` and `selected`. A single-Version response is unchanged.
- **Stream Token**: `fileFingerprint` binds direct, HLS, asset and sidecar grants to the exact file
  playbackInfo chose, minted only when that file is not the primary one. The byte routes resolve
  it within the grant's Media and fail closed when it is gone. Grants without it keep serving the
  primary file.
- **Web player**: a version menu when `versions` is present. The choice is remembered per profile
  and item in local storage and sent as `selectedVersion` on the next play.

## Not in this change

- Native client pickers (iPhone, macOS, Android phone and TV) land in their own repositories
  against this wire. tvOS is CORE-1466.
- Edition metadata (CORE-0204, CORE-0777) and keeping track choices across a Version switch
  (CORE-0484).

## Impact

- `internal/scan`, `internal/store` (new `media_versions.go`, no migration), `internal/playback`,
  `internal/streamtoken`, `internal/mediahttp`, `internal/web/app`.
- Wire changes are additive: wire contract minor bump (v2.5.0).
