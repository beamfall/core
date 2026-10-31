package store

// schemaFreezeCatalogMigrations is the ADR-0011/FREEZE-4 catalog marker.
//
// The catalog is append-only after freeze: existing migration SQL is never
// rewritten, new schema lands as the next embedded migration, and entity-query
// hot-path changes require benchmark evidence before this marker moves.
//
// Reservation audit, compacted from the former monolithic schema_freeze.go:
//   - 0057-0067 added recoverable Entity trash, sidecars, private subscription
//     auth references, push/extension state, Marker provenance/suppression,
//     decode state, encrypted-library attributes, and scrobble state.
//   - 0068-0079 narrowed skip kinds; added Profile ownership, playback
//     renditions, Beam handoff, import provenance, stream metadata, backup
//     policy, tags, navigation overrides, Collections, and Marker trash.
//   - 0080-0090 added Integration Worker state/signing, Stations, Offline
//     Packs, access visibility/requests/grants, feed GUID scheme, device
//     distribution class, node-host registry, and last-seen indexing.
//   - 0091-0098 added Offline Pack invariants, Beam reveal class, import and
//     unmatched provenance, field provenance/search policy, stream
//     dispositions, photo search support, encrypted relation indexes, and
//     Channels.
//   - 0099 added the `ad` Marker kind, internal flavor, and never-served
//     segment_detection evidence.
//   - 0100 added subtitle_choice_record.
//   - 0101 added subtitle_offset_nudge.
//   - 0102 added unmatched_suppression.
//   - 0103 added Offline Pack grant protocol bindings (ADR-0157/0208).
//   - 0106 added compat_decision_invalidation — durable refusal tombstones so a
//     restart cannot revive a pre-binding Stream Token — and 0107 added its
//     staged-promotion table making invalidation batches atomic across process
//     death (HANDSHAKE-0063-COMPAT-DECISION-DURABILITY-1 / ADR-0063; each rode
//     its own `make bench` gate at landing). Ordinals 0104/0105 in older prose
//     refer to these tables' pre-renumber names.
//   - 0108 adds immutable detection_run receipts, the append-only catalog
//     lifecycle ledger, candidate boundary/served evidence, and nullable
//     originating-run links on segment_detection and detected Marker rows
//     (PODAD-2-PROVENANCE-1 / ADR-0179 §2 / ADR-0193 Decisions 3/5). The tables
//     are off the entity-query hot path and keyed by Media/method or exact model
//     identity.
//   - 0109 adds the closed device_surface binding used to fail native-client
//     compatibility decisions closed.
//   - 0110 adds the integration Session kind, nullable device binding for that
//     kind, immutable integration scope set, and per-Profile opt-in.
//   - 0111 adds Circle membership, owner-created shadow-principal mappings,
//     disposable session cache, and durable revoke-notice obligations
//     .
//
// `make bench` passed on 2026-08-11 for the 0108 bump; the worst measured
// query was search/fts/revealed at 30.875084 ms against a 60 ms budget (P95
// across the full bench suite). The original transcript was lost before landing;
// these are the numbers reported at landing time.
const schemaFreezeCatalogMigrations = 107
