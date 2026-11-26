## 1. Encrypted Library Identification and Review Planes (`ADR-0045`)

- [x] 1.1 Add `encrypted_library` `0016_identification_state.sql` and route `media_external_id`, `artwork_candidate`, and `unmatched_queue` reads/writes to the owning plane; omit shared `media.updated` outbox events and clear `target_id` on encrypted artwork activity rows
- [x] 1.2 Add `encrypted_library` `0017_unmatched_review.sql` and route `unmatched_record` and `unmatched_suppression` reads/writes to the owning plane; refuse suppressions whose `RecordID` no readable plane holds
- [x] 1.3 Cover writer-owned `entity_relation` attributes on the Encrypted Library plane and sealed-plane refusals in store tests

## 2. Atomic Identification Commits and Durable Artwork Actions

- [x] 2.1 Add `catalog` `0141_identification_revision.sql` and `encrypted_library` `0018_identification_revision.sql` (`media_metadata_provenance`, `media_operator_ordinal`, immutable `media_identification_revision` with `request_id`/`request_digest`)
- [x] 2.2 Implement `CommitMediaIdentification` and `CommitFixMatch` (`internal/store/identification_revision.go`, `internal/identify/identify.go`, `internal/graph/graph_identify.go`) with idempotent replay, conflict detection, and no-Media-ID fixed-match logging
- [x] 2.3 Add `catalog` `0142_artwork_action.sql` and `encrypted_library` `0019_artwork_action.sql` (`media_artwork_action`), `CompleteArtworkAction` compare-and-set, and startup `ReplayArtworkActions` (`internal/app/app.go`); seed composite FK fixture parents in `backup_fixture_test.go`

## 3. Exact Feed GUIDs, Collision Review, and Encrypted Subscription Refusals

- [x] 3.1 Add `catalog` `0140_subscription_entry_collision.sql` (`WITHOUT ROWID`), keep feed entry GUIDs exact without trimming across `subscriptions.go`, `media_external_ids.go`, `provisional.go`, and `item_state_import.go`, and record collisions via `ErrSubscriptionEntryCollision` / `SubscriptionEntryCollisions`
- [x] 3.2 Refuse Encrypted Library Subscriptions (`ErrEncryptedLibrarySubscription`), poller runs (`SubscriptionPrivacyCheck` in `internal/ingest/ingest.go`), provisional feed Media (`ErrEncryptedLibraryProvisionalMedia`), and encrypted item-state imports

## 4. Profile Erasure and Failure-Atomic Unseal Evacuation

- [x] 4.1 Add `encrypted_library` `0020_subscription_evacuation.sql`, evacuate stranded identification, unmatched review, and legacy subscription/item-state rows in `UnsealEncryptedLibraryDB`, delete all shared rows in one `PRAGMA secure_delete = ON` transaction (`commitEvacuationPlansLocked`), and clear empty pending markers
- [x] 4.2 Extend Profile deletion erasure markers (`internal/store/privacy_delete.go`) to clear `media_operator_ordinal.actor_profile_id` on unsealed and sealed Encrypted Library planes
- [x] 4.3 Add SQL fault-injection, crash-convergence, concurrency, and refusal tests across `internal/store`, `internal/identify`, and `internal/ingest`

## 5. Gate and archive

- [ ] 5.1 `make gate`
- [ ] 5.2 Archive the change
