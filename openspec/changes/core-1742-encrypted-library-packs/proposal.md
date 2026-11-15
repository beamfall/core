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

## Not in this change

Packs at `high`, `medium` or `low` quality in an encrypted Library are still refused. Building them
without plaintext leaving the protected plane needs FFmpeg to read the source over a pipe (the
confined `Convert` is path-only, and the MP4/MOV demuxer needs a seekable input) and needs a
protected-plane location or at-rest encryption for the transcode output and temp files. Today the
pack directory is the public `DataDir/offline-packs`, and sealing re-reads sources through the
decrypt reader. That work stays open as a follow-up.

## Impact

- Store: `internal/store/entity.go`, `entity_artwork_source.go`, `offline_pack.go`, catalog
  migration 0115, schema-freeze marker 111.
- Offline packs: `internal/offlinepack/service_state.go`.
- No GraphQL or wire contract change.
