# List building offline packs with progress and failure

Ticket: CORE-1743 (`queue:core:main`).

## Why

A quality pack (CORE-1677) is built by a background job under a PackID assigned at request time,
but `offlinePacks` only lists packs once they are sealed. Until then a client cannot show that a
download is in progress, how far it has got, or that it failed and why. A failed build simply
disappears, so the only recovery is a fresh request under a new ID.

## What Changes

- New catalog migration 0116 adds `offline_pack_build`: one durable row per PackID while its build
  is `building` or `failed`, with coarse progress, a closed failure reason, and the job payload so a
  retry can rebuild it. Sealing the pack deletes the row in the same transaction; a Profile's privacy
  delete removes its rows.
- The build job records progress as it hashes, transcodes and seals items, and on a terminal error
  marks the row `failed` with one of `empty_target`, `unknown_target_kind`, `unknown_quality`,
  `device_attestation_failed`, `source_unavailable`, `transcode_failed`, `byte_bound_exceeded` or
  `build_failed`. A cancelled job stays `building` and reruns.
- `OfflinePackInfo` gains `progress` (0..1; a sealed pack reports 1) and optional `failureReason`.
  `offlinePacks` lists the caller's building and failed builds before its sealed packs.
- New session mutation `retryOfflinePack(id)`: restarts a failed build of the caller's device under
  the same PackID and queues one job. A retry of a build that is already running queues nothing, and
  a retry of a sealed pack returns it unchanged, so the mutation is idempotent.
- `revokeOfflinePack` on a failed build dismisses it; on a running build it is refused.

## Non-goals

- No new wire contract version is published; the fields are additive.
- Progress is step-based (per hashed, transcoded and sealed item), not byte-accurate.
- Builds without a pre-assigned PackID (the synchronous original-quality path) are not tracked.
