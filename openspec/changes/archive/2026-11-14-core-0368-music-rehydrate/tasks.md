## 1. Scan

- [x] 1.1 `MetadataHydrationInput.ContentFingerprint`: pass the computed hash on the full import path and the stored fingerprint on the backfill path
- [x] 1.2 Tests: the hook receives the file's fingerprint, and a re-tagged file reaches it with the new one

## 2. Hydration seam

- [x] 2.1 Optional mapper interface naming the fingerprint field and deciding Fresh, Skip or Replace
- [x] 2.2 `HydrateMetadata`: read the recorded fingerprint; Skip writes nothing, Fresh writes absent-only, Replace upserts at the scan tier without overriding locked, operator or manual fields; both writes record the fingerprint
- [x] 2.3 Tests against a real store: re-tag shows new values, a locked title survives, a manual title survives, an unchanged file is not re-hydrated
- [x] 2.4 Unreadable field locks fail closed: the write drops to absent-only and the recorded fingerprint stays

## 3. Composition

- [x] 3.1 `cmd/beamfall/hooks_shared.go`: the music mapper implements the optional interface with `music.HydratedFingerprintField` and `music.DecideHydrationWrite`

## 4. Follow-up outside this change

- [ ] 4.1 Media identity continuity: new bytes at the same path derive a new content-fingerprint
  Media ID, so a full-scan re-tag starts a fresh Media and a lock or manual edit stays on the old one
