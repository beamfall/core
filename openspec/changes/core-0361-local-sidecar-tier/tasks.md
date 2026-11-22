## 1. Store

- [x] 1.1 `local` mutation source and rank (manual > local > operator), rank SQL in lockstep, local fields read as locked
- [x] 1.2 `ApplyLocalMetadata`: release, local writes, owned-relation reconcile and signature in one transaction on the Media's catalog plane
- [x] 1.3 `ReleaseLocalMetadata`: a removed sidecar's fields and relation rows released, empty signature recorded, one transaction
- [x] 1.4 A scraper's relation upsert leaves a `local` row alone

## 2. Hydration seam

- [x] 2.1 `HydrationSidecarMapper.MapSidecar` local part, validated against the bundle; owned relations must be declared Media relations
- [x] 2.2 Unreadable sidecars release and record nothing; the recorded signature is the plan's
- [x] 2.3 The Media's sidecar comes from the first of its files, in path order, that has one
- [x] 2.4 Field-less Entities are ensured, not rewritten

## 3. Scan and composition

- [x] 3.1 The scan refreshes sidecars for unchanged files and attached versions without a probe
- [x] 3.2 The Film & TV mapper passes its sidecar hydration, signature and owned relations through the seam; migrate passes the selected categories
- [x] 3.3 Tests per finding: manual credit kept, emptied relation, encrypted library, failed apply keeps locks, plan signature, unreadable, secondary version, probe-free removal, scraper takeover, category scope
