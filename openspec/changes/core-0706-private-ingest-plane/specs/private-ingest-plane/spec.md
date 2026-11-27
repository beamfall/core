## ADDED Requirements

### Requirement: Per-Media identification state lives on the owning plane

Per-Media identification state (`media_external_id`, `artwork_candidate`, `unmatched_queue`,
provenance, operator ordinal, identification revisions, and artwork actions) SHALL be stored on
the Media's owning Library plane. Sealed or unknown Media writes SHALL be refused without touching
`catalog.db`, and encrypted-plane writes SHALL NOT emit shared `media.updated` outbox events.

#### Scenario: Encrypted Library identification stays off the shared catalog

- **WHEN** an unsealed Encrypted Library's Media receives external IDs, artwork candidates,
  unmatched queue state, metadata provenance, or an identification revision
- **THEN** those rows are written only to that Library's private plane and `catalog.db` and the
  shared event outbox carry no Media ID, title, or candidate ID for that Media

#### Scenario: Sealed Encrypted Library refuses identification writes

- **WHEN** an Encrypted Library plane is sealed and an identification, external ID, or unmatched
  queue write targets one of its Media
- **THEN** the write is refused and nothing is persisted in `catalog.db`

### Requirement: Cross-Media identification reads skip sealed planes

Cross-Media identification reads SHALL combine `catalog.db` with every unsealed Encrypted Library
plane and SHALL omit sealed planes. Encrypted artwork upload activity rows SHALL clear `target_id`.

#### Scenario: Sealed plane rows are excluded from cross-Media reads

- **WHEN** an Encrypted Library is sealed after storing external IDs, artwork candidates, or
  unmatched queue rows
- **THEN** cross-Media lookups and `UnmatchedItems` omit its rows until the plane unseals again

### Requirement: Unmatched review records and suppressions isolate by Library plane

Unmatched records and their scoped suppressions for an Encrypted Library SHALL reside on that
Library's plane (`encrypted_library` `0017`). Writes for a sealed Library SHALL fail with
`ErrEncryptedLibraryDBDisabled`, and suppressions naming a `RecordID` SHALL write to that record's
plane or fail with `ErrUnmatchedRecordNotFound`, never falling back to `catalog.db`.

#### Scenario: Record-scoped suppression requires a readable plane

- **WHEN** a caller suppresses a record whose `RecordID` sits on a sealed Encrypted Library or does
  not exist on any readable plane
- **THEN** `SuppressUnmatched` refuses the suppression and writes nothing to `catalog.db`

#### Scenario: Unsealed Encrypted Library review stays private

- **WHEN** an unmatched record and its suppression are created for an unsealed Encrypted Library
- **THEN** both rows live on that Library's plane and `catalog.db` holds neither row

### Requirement: Identification and Fix Match commit atomically with immutable revisions

`CommitMediaIdentification` and `CommitFixMatch` SHALL commit metadata, lock clears, operator
ordinal, provenance, plugin fields, and (for Fix Match) `match_pinned = 1` on fingerprinted files
plus `unmatched_queue` clearing alongside an immutable `media_identification_revision` row in one
plane transaction. Updates to revisions SHALL be rejected by trigger.

#### Scenario: Refused Fix Match changes nothing

- **WHEN** `CommitFixMatch` fails visibility or lock validation
- **THEN** metadata, match pins, `unmatched_queue`, and revisions are all unchanged

#### Scenario: Revisions are immutable

- **WHEN** a caller attempts to `UPDATE` a row in `media_identification_revision`
- **THEN** the database trigger aborts the update

### Requirement: Identification requests are idempotent on request ID

Replaying a committed `request_id` with an identical content digest SHALL return the original
revision with `Replayed: true` without writing. Reusing a committed `request_id` with different
content or identification kind SHALL fail with `ErrIdentificationRequestConflict`.

#### Scenario: Concurrent replays converge to one revision

- **WHEN** concurrent callers submit `CommitMediaIdentification` or `CommitFixMatch` with the same
  `request_id` and identical payload
- **THEN** exactly one revision row is committed and every caller receives that same revision number

#### Scenario: Reused request ID with different content is refused

- **WHEN** a caller submits a committed `request_id` with different metadata, provenance, fields, or
  identification kind
- **THEN** the commit fails with `ErrIdentificationRequestConflict` and the earlier commit stands

### Requirement: Planned identification artwork fetches persist as durable actions

An identification transaction with scheduled artwork SHALL record each slot as a `pending` row in
`media_artwork_action` on the Media's plane and mark prior `pending` rows for that Media
`superseded`. Workers SHALL complete actions via compare-and-set (`pending` to `done`), and
startup SHALL replay remaining `pending` actions across `catalog.db` and unsealed planes.

#### Scenario: Restart replays pending identification artwork once

- **WHEN** an identification commits pending `poster` and `backdrop` actions and the process
  restarts before fetching them
- **THEN** `ReplayArtworkActions` fetches and completes each pending action once, and a second
  replay finds zero pending actions

#### Scenario: Newer revision with artwork supersedes prior pending actions

- **WHEN** a second identification scheduling artwork commits before an earlier revision's pending
  artwork actions have completed
- **THEN** the earlier pending actions transition to `superseded` and only the newer revision's
  actions remain `pending`

### Requirement: Encrypted Libraries refuse plaintext Subscriptions and provisional Media

Creating, resolving, polling, or writing entries or titles for an Encrypted Library Subscription
SHALL fail with `ErrEncryptedLibrarySubscription`. Creating or registering provisional feed Media
(`ErrEncryptedLibraryProvisionalMedia`) and recording item-state imports for an Encrypted Library
Subscription SHALL likewise be refused before writing feed URLs, credentials, GUIDs, or paths to
`catalog.db`.

#### Scenario: Encrypted Library Subscription creation and polling are refused

- **WHEN** a caller creates a Subscription or polls a legacy Subscription belonging to an Encrypted
  Library
- **THEN** the operation fails with `ErrEncryptedLibrarySubscription` before fetching the feed or
  writing rows to `catalog.db`

#### Scenario: Encrypted Library provisional Media is refused

- **WHEN** a caller creates or registers a provisional feed Media in an Encrypted Library
- **THEN** the operation fails with `ErrEncryptedLibraryProvisionalMedia` and writes no `media`,
  `media_file`, or `media_external_id` rows to `catalog.db`

### Requirement: Feed entry GUIDs remain exact and collisions are recorded for review

Feed entry GUIDs, `feed-guid` and `provisional-guid` external IDs, `ProvisionalMediaID`
derivations, and `ItemStateImport` GUIDs SHALL keep exact bytes without whitespace trimming. When
`AssociateSubscriptionEntryMedia` sees a GUID bound to a different path, it SHALL preserve the
binding, upsert `subscription_entry_collision`, and return `ErrSubscriptionEntryCollision`.

#### Scenario: Exact feed GUID round-trips with whitespace intact

- **WHEN** a feed entry GUID with leading or trailing whitespace is marked seen, associated, or
  used to create provisional Media
- **THEN** the exact untrimmed GUID is retained in receipts, external IDs, and provisional Media IDs

#### Scenario: Same GUID offered for a second file records a collision

- **WHEN** `AssociateSubscriptionEntryMedia` is called for a GUID already bound to path A with
  Media at path B
- **THEN** the call returns `ErrSubscriptionEntryCollision`, path A stays bound in
  `subscription_seen_entry`, and `subscription_entry_collision` records and counts path B for
  operator review

### Requirement: Profile deletion erases Encrypted Library plane footprints

Full Profile deletion and `DeleteProfile` SHALL record durable per-Encrypted-Library erasure markers
in the catalog transaction, immediately clear owed rows (including
`media_operator_ordinal.actor_profile_id`) on unsealed planes, and apply owed erasures to sealed
planes at their next unseal before clearing the markers.

#### Scenario: Sealed plane erases deleted Profile actor on unseal

- **WHEN** a Profile recorded as `actor_profile_id` on an Encrypted Library Media's operator ordinal
  is deleted while the plane is sealed and the plane is later unsealed
- **THEN** the unseal clears `actor_profile_id` on that plane and removes the erasure marker

### Requirement: Unseal evacuation copies privately before deleting shared rows in one transaction

During `UnsealEncryptedLibraryDB`, stranded identification state, unmatched review rows, legacy
Subscription and item-state rows (`encrypted_library` `0020`), and owed Profile erasures SHALL copy
idempotently to the private plane before all shared deletes commit child-first under
`PRAGMA secure_delete = ON` in one transaction. Any copy or shared-delete failure SHALL abort unseal
with shared rows intact; post-commit marker cleanup errors retry on the next unseal.

#### Scenario: Shared delete fault unwinds unseal without losing rows

- **WHEN** private copies commit during unseal and the subsequent shared `catalog.db` delete fails
- **THEN** the unseal fails, the plane remains unregistered, the shared rows remain in `catalog.db`,
  and the next unseal after the fault clears leaves one copy on the private plane and zero rows in
  `catalog.db`

#### Scenario: Legacy Encrypted Library subscription rows evacuate once at unseal

- **WHEN** pre-release `subscription`, `subscription_seen_entry`, `subscription_entry`,
  `subscription_entry_collision`, and `item_state_import` rows for an Encrypted Library exist in
  `catalog.db` and the Library is unsealed
- **THEN** all five tables move to the plane's `0020` holding tables, `catalog.db` rows are deleted
  under `secure_delete`, and the `legacy_subscription_evacuation` marker is cleared
