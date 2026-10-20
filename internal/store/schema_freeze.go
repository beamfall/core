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
//   - events 0014 added the engagement event log.
//   - events 0015 added scoped API-key activity attribution (ADR-0040 §2.2 /
//     ADR-0012 §11); events 0016 (MUSIC-6, ADR-0098 area 6) added
//     pack_token_ledger.
//   - events 0017 added run-scoped import provenance;
//     events 0018 added the materialized-read
//     projection; events 0019 added nullable engagement
//     columns; events 0020 added
//     playback_progress.channel_context.
//   - events 0021 (AUDITFIX-SCHEDULER-DURABILITY) added the schedule
//     occurrence journal (schedule_occurrence, schedule_occurrence_member) so
//     partial-enqueue progress survives restart and recovers exactly once; it
//     lives beside the job table (member publication commits atomically with
//     the job insert) and sits entirely off the entity-query hot path.
//   - encrypted-library 0001-0005 (ENC-1/ENC-4/ENC-ENTITY-RELATION-UPSERT-1,
//     ADR-0045) mirror the typed Media-field, provenance, and entity-relation
//     upsert surface into the encrypted library.
//
// Every entry above rode the `make bench` gate at the migration that introduced
// it, and each sits off the entity-query hot path.
//
// Historical vocabulary-firewall anchors remain here because conformance pins
// their exact narrow exceptions:
//   - mute/retention/podcast predicates
const (
	schemaFreezeEventsMigrations           = 21
	schemaFreezeEncryptedLibraryMigrations = 5
)
