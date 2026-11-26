package store

// Schema freeze — ADR-0011 §2 / roadmap FREEZE-4.
//
// The 100k-Media / 10k-Entity / 2M-relation benchmark (`make bench`,
// TestEntityQueryLatencyBudget) passed within every P95 budget on 2026-06-15,
// AFTER the FREEZE-1 (1:N media_file Versions/Parts), FREEZE-2 (Editions/Extras
// + media_relation), FREEZE-3 (engagement provenance + import match-pin) schema
// reservations and PIPE-1 (unmatched_queue) landed. The storage schema is
// therefore formally FROZEN at the migration counts below.
//
// Pre-freeze reservation audit (FREEZE-4 acceptance):
//   - FREEZE-1 / FREEZE-2 / FREEZE-3 — IN (merged).
//   - BF-1 markers Source — IN (marker table 0017 + markersQuery + CRUD).
//   - FND-A (per-Profile cross-DB Source predicate seam) — CONSCIOUSLY DEFERRED:
//     it is a query-layer seam over already-frozen tables (catalog + the events
//     playback-progress projection), not a schema reservation; its consumers
//     (mute/retention/podcast predicates) are post-parity. A future index it may
//     want rides the benchmark gate like any post-freeze change.
//
// ADR-0011 §2: "the last-schema claim is only as good as this [benchmark] gate."
// A NEW migration past the freeze is permitted, but it is a DELIBERATE act:
// re-run `make bench`, confirm the P95 budgets still hold, then bump the matching
// constant below in the SAME commit. TestSchemaFreeze enforces that conscious
// bump so the schema can never drift past its last benchmarked shape silently.
// The count marker does not detect rewritten history or destructive changes
// hidden behind a matching bump; VerifyFrozenSchema separately pins migration
// checksums and the minimum table/column surface.
//
// The per-migration catalog reservation audit moved verbatim-in-substance to
// schema_freeze_catalog.go alongside the catalog marker it justifies; this file
// keeps the freeze doctrine above and the non-catalog markers below. The split
// is presentational — TestSchemaFreeze reads the same three constants.
//
// Non-catalog reservation audit:
//
//   - events 0014 added the engagement event log.
//
//   - events 0015 added scoped API-key activity attribution (ADR-0040 §2.2 /
//     ADR-0012 §11); events 0016 (MUSIC-6, ADR-0098 area 6) added
//     pack_token_ledger.
//
//   - events 0017 added run-scoped import provenance;
//     events 0018 added the materialized-read
//     projection; events 0019 added nullable engagement
//     columns; events 0020 added
//     playback_progress.channel_context.
//
//   - events 0021 (AUDITFIX-SCHEDULER-DURABILITY) added the schedule
//     occurrence journal (schedule_occurrence, schedule_occurrence_member) so
//     partial-enqueue progress survives restart and recovers exactly once; it
//     lives beside the job table (member publication commits atomically with
//     the job insert) and sits entirely off the entity-query hot path.
//
//   - events 0022 (CORE-1194) added the per-Profile watchlist_item and
//     item_rating tables; both are keyed by Profile and read only by their own
//     Sources, off the entity-query hot path.
//
//   - events 0023 (CORE-2305) added stream_token_ledger.last_active_ms so the
//     live-sessions read can drop playbacks with no recent activity; it is
//     written on grant issue and progress reports and read only by the
//     active-session reads, off the entity-query hot path.
//
//   - encrypted-library 0001-0005 (ENC-1/ENC-4/ENC-ENTITY-RELATION-UPSERT-1,
//     ADR-0045) mirror the typed Media-field, provenance, and entity-relation
//     upsert surface into the encrypted library.
//
//   - encrypted-library 0006 (CORE-1626) mirrors catalog 0115's gapless
//     media_file_stream columns into the encrypted library.
//
//   - encrypted-library 0007 (CORE-1963) mirrors catalog 0070's
//     playback_rendition table into the encrypted library so precomputed
//     rendition rows for an Encrypted Library never reach the shared catalog;
//     it is read only by the precompute path, off the entity-query hot path.
//     The latency bench passed for the 0007 bump on 2026-11-15; the worst
//     measured query was search/fts/revealed at 28.48125 ms against a 60 ms
//     budget, at a host load average near 30.
//
//   - encrypted-library 0008 (CORE-2055) additively mirrors catalog 0050's
//     media_file_stream columns (stream_type and the codec/disposition/HDR
//     facts) and its (content_fingerprint, filename) font-attachment key, so
//     the shared inventory writer works on an Encrypted Library; the frozen
//     0002 columns stay. Both tables are read per file, off the entity-query
//     hot path.
//
//   - encrypted-library 0009 (CORE-2554) mirrors catalog 0123's
//     media_field_provenance.parser_owned marker; it is read only by the scan
//     write, off the entity-query hot path.
//
//   - encrypted-library 0010 (CORE-2583) mirrors catalog 0124's durable
//     Media insertion sequence and insert/delete triggers.
//
//   - encrypted-library 0011 (CORE-2380) adds source_end_observation in the
//     owning Library plane. The same 2026-11-18 benchmark recorded beside the
//     catalog marker passed; source fingerprints never enter the shared DB.
//
//   - encrypted-library 0012 (CORE-3152) mirrors catalog 0131's nullable
//     entity_relation.writer column, read only by the identify cleanup.
//
// Every entry above rode the `make bench` gate at the migration that introduced
// it, and each sits off the entity-query hot path.
//
// Historical vocabulary-firewall anchors remain here because conformance pins
// their exact narrow exceptions:
//   - mute/retention/podcast predicates
//
// Events 0024 (CORE-2762) stages Version merge playback for recovery and
// Profile erasure without changing entity queries.
//
// Events 0025 (CORE-2039) adds playback_progress.continue_hidden so a Profile
// can remove an item from Continue Watching and keep its resume position; it
// is read only by the continue-watching Source, off the entity-query hot path.
// Events 0026 (CORE-1630) gives Play Queue entries stable IDs and persists
// Profile playback state without changing entity queries.
// Events 0027 (CORE-1662) adds party sessions, participants, entries and
// screens without changing entity queries.
// Events 0028 (CORE-3182) adds the offline engagement replay ledger, written
// only by the offline replay mutation, off the entity-query hot path.
// Encrypted-library 0012 (CORE-3152) adds the entity_relation writer column.
// Encrypted-library 0013 (CORE-3131) keeps relation locks and recovery rows
// in the owning Library plane.
// Encrypted-library 0016 (CORE-0706) adds media_external_id,
// artwork_candidate and unmatched_queue to the owning Library plane, off the
// entity-query hot path.
// Events 0029 (CORE-2958) adds the per-follow new-entry route, read by the
// subscription-new Source and the poller, without changing entity queries.
// Events 0030 (CORE-3309) adds renderer_session_revision and the 24h
// renderer_session_command ledger for revision-checked session control, off the
// entity-query hot path.
// Events 0031 (CORE-3310) adds player_commanded_media for play-media command
// authorization on player-bound Sessions with walk-up disabled, off the
// entity-query hot path.
// Events 0032 (CORE-3312) extends player_commanded_media with originator/generation
// attribution and adds playback_grant + playback_grant_report for server-bound
// display playback attribution and idempotent report handling, off the
// entity-query hot path.
// Events 0033 (CORE-3313) adds auth_session_id, device_id,
// binding_generation, and chosen_lead to party_screen for session-bound
// stage reads, player display joining, and release tracking, off the
// entity-query hot path.
// Events 0034 (CORE-3379) adds party_screen.qr_corner and muted, the host's
// per-screen join-QR corner and sound setting, read only by party reads, off
// the entity-query hot path.
// Encrypted-library 0014 (CORE-0768) mirrors catalog 0078's collection and
// collection_media in the owning Library plane, read only by Collection reads
// and the collection Source, off the entity-query hot path.
// Encrypted-library 0015 (CORE-0769) mirrors the catalog saved_filter,
// smart_collection, saved_station and channel tables in the owning Library
// plane, read by their own by-ID and per-Library list reads, export and My
// Data, off the entity-query hot path.
// Encrypted-library 0017 (CORE-0706) mirrors catalog 0094's unmatched_record
// and 0102's unmatched_suppression in the owning Library plane, read only by
// the review inbox and the re-scan suppression check, off the entity-query hot
// path.
// Encrypted-library 0018 (CORE-0706) mirrors catalog 0113/0119 provenance,
// 0132's operator ordinal and 0140's identification revisions in the owning
// Library plane, written by identification commits and read by Fix Match and
// provenance reads, off the entity-query hot path.
// Encrypted-library 0019 (CORE-0706) mirrors catalog 0141's pending artwork
// actions in the owning Library plane, written by identification commits and
// read by the post-commit artwork worker and its restart replay.
// Encrypted-library 0020 (CORE-0706) adds tables for legacy Subscription and
// item-state import rows evacuated at unseal; 0021 adds artwork fetch retry
// attempts/backoff, 0022 permits Profile actor erasure on immutable
// identification revisions, and 0023 adds Subscription uniqueness/entry
// indexes and plane-local retention activity staging (CORE-3511), all off the
// entity-query hot path.
const (
	schemaFreezeEventsMigrations           = 34
	schemaFreezeEncryptedLibraryMigrations = 23
)
