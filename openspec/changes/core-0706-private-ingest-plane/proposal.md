# Private ingest, identification plane isolation, and durable identification commits

Ticket: CORE-0706 (`queue:core:main`).
Design: `ADR-0045` (dual catalog planes), `ADR-0068` (unmatched review and Fix Match), `ADR-0052` (provisional feed Media).

## Why

Identification state, unmatched review records, and feed-ingest metadata for an Encrypted Library's
Media leaked into the shared `catalog.db` (or lacked sealed-plane gating and unseal evacuation),
and identification commits lacked an atomic, idempotent revision boundary with durable post-commit
artwork action tracking. Meanwhile, trimmed feed GUID keys prevented exact matching and dropped
same-GUID collisions instead of surfacing them for review.

## What Changes

- **Encrypted Library plane isolation (`ADR-0045`)**:
  - `encrypted_library` migrations `0016`–`0019` mirror `media_external_id`, `artwork_candidate`,
    `unmatched_queue`, `unmatched_record`, `unmatched_suppression`, `media_metadata_provenance`,
    `media_operator_ordinal`, `media_identification_revision`, and `media_artwork_action` on each
    Library's own protected plane.
  - Per-Media identification reads/writes, unmatched review records, and record-scoped
    suppressions route to the owning plane and refuse sealed or unreadable planes without falling
    back to `catalog.db`. Encrypted-plane identification and external-ID writes emit no shared
    `media.updated` outbox event, and artwork upload activity rows clear `target_id`.
- **Atomic, idempotent identification commits and durable artwork actions**:
  - `catalog` migrations `0141`–`0142` (and `encrypted_library` `0018`–`0019`) add immutable
    `media_identification_revision` rows with `request_id` / `request_digest` idempotency receipts
    and `media_artwork_action` rows for post-commit artwork fetches.
  - `CommitMediaIdentification` and `CommitFixMatch` apply metadata, lock clears, operator ordinal,
    provenance, plugin fields, match pins, queue resolution, immutable revision audit, and pending
    artwork actions in one transaction on the Media's own plane.
  - Artwork workers complete pending actions via compare-and-set (`pending` -> `done`); a newer
    revision that schedules artwork supersedes older pending actions, and startup replays remaining
    pending actions once.
- **Exact feed GUIDs, collision review, and Encrypted Library feed refusal**:
  - Feed GUID keys (`subscription_seen_entry`, `feed-guid`, `provisional-guid`, `ItemStateImport`,
    and `ProvisionalMediaID`) preserve exact bytes without trimming.
  - `catalog` migration `0140` adds `subscription_entry_collision` (`WITHOUT ROWID`) to retain
    same-GUID, different-file associations with observation counts for review instead of dropping them.
  - Encrypted Libraries refuse new/legacy Subscriptions (`ErrEncryptedLibrarySubscription`),
    provisional feed Media (`ErrEncryptedLibraryProvisionalMedia`), and encrypted item-state
    imports before writing feed URLs, credentials, GUIDs, enclosure URLs, or paths to `catalog.db`.
- **Profile erasure and failure-atomic unseal evacuation**:
  - Profile deletion records per-Encrypted-Library erasure markers (`DeleteProfile` /
    `DeleteProfileFootprintAudited`) and clears `media_operator_ordinal.actor_profile_id`
    alongside plane expressions, immediately for unsealed planes and at next unseal for sealed ones.
  - Unseal (`UnsealEncryptedLibraryDB`) copies stranded identification rows, unmatched review
    rows, legacy subscription/item-state rows (`encrypted_library` `0020`), and owed profile
    erasures to the private plane first, then executes all shared `catalog.db` deletes under
    `PRAGMA secure_delete = ON` in a single shared transaction (`commitEvacuationPlansLocked`),
    clearing pending evacuation markers even when no rows remain to move.

## Non-goals

- Supporting live feed Subscriptions, provisional feed Media, or item-state imports inside
  Encrypted Libraries (those remain explicitly refused until full private-plane feed polling is built).
- Opening GraphQL/web Fix Match reads and mutations for Encrypted Libraries (`fixMatchMedia` keeps
  its `fix_match.encrypted_library` refusal per `web-unmatched-fix-flows`).
- Changing the wire contract or GraphQL schema.

## Capabilities

### New Capabilities

- `private-ingest-plane`: Encrypted Library plane isolation for identification and review state,
  atomic idempotent identification commits with durable artwork actions, exact feed GUID keys and
  collision review, Encrypted Library feed/provisional refusals, and failure-atomic unseal
  evacuation.

### Modified Capabilities

None.

## Failure modes

- **Evacuation copy or delete failure**: if copying onto the private plane or the single shared-delete
  transaction fails (or the process crashes between them), unseal aborts leaving the plane
  unregistered and `catalog.db` rows intact; the next unseal retries idempotently (`INSERT OR IGNORE`)
  and converges with one private copy and zero shared rows.
- **Interrupted artwork fetch**: if the server stops after committing an identification revision and
  before completing artwork downloads, `media_artwork_action` preserves the pending slots for
  startup replay (`ReplayArtworkActions`), and a newer revision scheduling artwork marks prior
  pending slots `superseded`.
- **Reused request ID**: presenting a previously committed `request_id` with matching content replays
  the existing revision without writing; presenting the same `request_id` with different content
  fails with `ErrIdentificationRequestConflict`.
- **Sealed plane or missing record**: writing identification state, unmatched records, or
  record-scoped suppressions against a sealed or unreadable plane refuses rather than parking
  plaintext metadata in `catalog.db`.

## Acceptance evidence

- `internal/store`, `internal/identify`, `internal/ingest`, and `internal/graph` tests covering:
  - Private-plane identification, external IDs, artwork candidates, unmatched queue/records,
    suppressions, and writer-owned relations leaving zero rows in `catalog.db` or shared outbox,
    plus refusal on sealed planes.
  - Concurrent idempotency replay and conflict rejection on `CommitMediaIdentification` and
    `CommitFixMatch`, immutable revision trigger, rollback on refusal, and durable `media_artwork_action`
    replay/supersession.
  - Exact feed GUID round-trips, `ProvisionalMediaID` / promotion CAS convergence, durable
    `subscription_entry_collision` counting, and refusal of Encrypted Library Subscriptions,
    provisional Media, and item-state imports.
  - Deleted-Profile footprint erasure on unsealed and sealed Encrypted Library planes, and SQL
    fault-injection / crash-recovery tests across unseal evacuation boundaries.
- `/opt/homebrew/bin/openspec validate --all --strict` passes.

## Rollback

- Code changes revert cleanly via git revert.
- Applied `catalog` (`0140`–`0142`) and `encrypted_library` (`0016`–`0020`) migrations remain
  append-only per core migration invariants; rows already evacuated onto protected Library planes
  stay isolated there.

## Impact

- Store and migrations: `internal/store/migrate/catalog/0140`–`0142`,
  `internal/store/migrate/encrypted_library/0016`–`0020`, `internal/store/identification_*.go`,
  `internal/store/unmatched*.go`, `internal/store/subscriptions.go`, `internal/store/provisional.go`,
  `internal/store/item_state_import.go`, `internal/store/encrypted_library_evacuate.go`,
  `internal/store/privacy_delete.go`, `internal/store/backup_fixture_test.go`.
- Services: `internal/identify/identify.go`, `internal/graph/graph_identify.go`,
  `internal/ingest/ingest.go`, `internal/app/app.go`.
- No wire contract changes.
