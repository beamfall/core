## 1. Store

- [x] 1.1 `local` mutation source and rank (manual > local > operator), rank SQL in lockstep, local fields read as locked
- [x] 1.2 `ReleaseMediaFieldSource` moves one source's provenance to the scan tier, keeping values

## 2. Hydration seam

- [x] 2.1 `HydrationPlan.Local`, validated and written at the local tier with the previous local fields released first
- [x] 2.2 `HydrationSidecarMapper`: signature recorded after the writes; a gone sidecar releases the local fields
- [x] 2.3 Field-less Entities are ensured, Entities with fields merge over the stored ones

## 3. Scan and composition

- [x] 3.1 The scan re-hydrates an unchanged file whose sidecar signature changed
- [x] 3.2 The Film & TV mapper passes its sidecar hydration and signature through the seam
- [x] 3.3 Tests: identification keeps local values, a sidecar edit refreshes them, removal unlocks and keeps them, sidecar watch state reaches the importing profile only
