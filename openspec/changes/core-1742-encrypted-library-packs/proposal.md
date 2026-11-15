# Offline packs in unsealed encrypted Libraries

Ticket: CORE-1742 (`queue:core:main`).

## Why

Offline packs did not work in an encrypted Library even when it was unsealed and the caller could see
it. Season and series targets resolved to nothing, because the store reads they go through (the
Entities pointing at a parent, and an Entity's Media) queried only the catalog database, while an
encrypted Library keeps its Entities, relations and Media in its own database. Packs of encrypted
Media could not be saved either: `offline_pack_item.media_id` referenced the catalog `media` table,
so every encrypted item failed the foreign key. Original-quality builds also failed with "encrypted
item is missing catalog size", because the store's Media file reads never return `size_bytes` and
stat would report the ciphertext length.

## What Changes

- `EntitiesRelatingTo` and `MediaForEntity` run on the Entity's own plane, as `Entity` and
  `RelatedEntityGroups` already did. In an encrypted Library the certification filter is the
  plane-scoped variant, which never reads a `library` table that plane does not have.
- Catalog migration 0115 rebuilds `offline_pack_item` without its foreign key to catalog `media`.
  `CreateOfflinePack` now checks that each item names Media on a readable plane, so an item naming
  Media in a sealed Library or nowhere is still refused. A trigger keeps the old cascade, so deleting
  catalog Media still removes its pack items.
- When the catalog carries no size, an encrypted original item takes its plaintext length from the
  decrypt reader. The full hashing pass then decrypts and authenticates every chunk and refuses a
  stream that does not reach that length.
- A sealed or unauthorized Library still refuses: its Entities and Media are not found, and its
  Media cannot be frozen into a pack.

- Packs at `high`, `medium` or `low` quality in an encrypted Library build without plaintext leaving
  the protected plane (ADR-0235). A shared `internal/encderiv` helper serves the decrypted source
  from a loopback-only HTTP endpoint with a single-use 256-bit token, range reads, and revocation at
  job end, on expiry or idleness, or when the build loses its authorization. FFmpeg reads that
  endpoint (`http` only, no redirects) and writes fragmented MP4 to stdout. Core builds the `sidx`,
  stages the output encrypted under a per-item key in a `0600` `.stage` file, and seals the pack item
  from it. Staging is removed on every exit path, its key is zeroed, and startup sweeps any a crash
  left behind. A Seal, a disabled Profile or a withdrawn Library grant mid-build stops the transcode.

## Not in this change

Precompute, generated assets, subtitle extraction and live HLS keep their existing FFmpeg paths; the
`file`-only protocol allowlist stays for every caller but the pipe transcode.

## Impact

- Store: `internal/store/entity.go`, `entity_artwork_source.go`, `offline_pack.go`, catalog
  migration 0115, schema-freeze marker 111.
- Encrypted derivatives: new `internal/encderiv`.
- FFmpeg: `internal/ffmpeg` pipe transcode and its confinement; the `cmd/beamfall` FFmpeg shim takes
  `--require-landlock`.
- Offline packs: `internal/offlinepack/service_state.go`, `offlinepack.go`, `service_io.go`; app
  wiring in `internal/app`.
- No GraphQL or wire contract change.
