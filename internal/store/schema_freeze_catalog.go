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
//   - 0112 adds device_build_binding, the exact device-to-native-build
//     mapping ADR-0063 provenance requires (COMPAT-ENROLLMENT-BUILD-MAPPING-1).
//     It is read once per native compatibility handshake by primary key and
//     written once at enrollment, off every entity-query path.
//   - 0113 adds media_metadata_provenance, one row per identified Media item
//     naming the plugin scraper that supplied its metadata (CORE-1126). It is
//     keyed by Media ID, read only by the media(id) detail read, and off the
//     entity-query hot path.
//   - 0114 adds playback_keyframes, the disposable per-file keyframe index the
//     audio-copy HLS rung builds its playlist from (CORE-0021). It is read by
//     primary key once per audio-copy session start and written once per file
//     generation, off every entity-query path.
//   - 0115 rebuilds offline_pack_item without its catalog-only media foreign
//     key so a pack can hold Encrypted Library Media (CORE-1742); a trigger
//     keeps the catalog Media delete cascade. Same columns and index, off
//     every entity-query path.
//   - 0116 adds offline_pack_build, the pending row for an Offline Pack whose
//     background build has not sealed yet (CORE-1743). It is keyed by pack ID,
//     read only by the offlinePacks list and retry paths, written a few times
//     per build, and off every entity-query path.
//   - 0117 adds profile.remote_max_bitrate, the per-Profile remote playback
//     bitrate cap (CORE-1717). It is a defaulted scalar column read with the
//     Profile row at playback decision time; it adds no index and touches no
//     entity-query table.
//   - 0118 adds the gapless columns (sample rate, encoder delay, end padding,
//     decoded length) to media_file_stream (CORE-1626). They ride the
//     existing fingerprint-keyed inventory read once per playback decision;
//     no index and no entity-query table changes.
//   - 0119 adds media_metadata_provenance.candidate_id, the provider candidate
//     a per-item refresh re-runs, and media_sort_title, one row per Media item
//     with an admin-set sort title (CORE-1715). The sort-title table is read
//     once per catalog-plane library browse, by library, and is empty unless an
//     admin set a sort title; neither touches an entity-query path.
//   - 0120 adds api_token.access, the cli/diagnostics/agent tier that bounds
//     the Console fields a token runs (CORE-2095). It is read once per
//     token-authenticated request by the existing token-hash lookup; no index
//     and no entity-query table changes.
//   - 0121 adds library.show_on_home, the per-Library Home surfacing policy
//     (CORE-2360). It is read with the other Library columns and, for Home
//     shelf requests only, by one scan of the small library table; no index
//     and no entity-query table changes.
//   - 0122 adds media_file_stream.title, a stream's container title tag that
//     subtitle pickers label tracks with (CORE-2523). It rides the existing
//     fingerprint-keyed inventory read; no index and no entity-query table
//     changes.
//
// `make bench` passed on 2026-08-11 for the 0108 bump; the worst measured
// query was search/fts/revealed at 30.875084 ms against a 60 ms budget (P95
// across the full bench suite). The original transcript was lost before landing;
// these are the numbers reported at landing time.
//
// The latency bench (`make bench`'s TestEntityQueryLatencyBudget) passed on
// 2026-11-13 for the 0112 bump; the worst measured query was search/fts/revealed
// at 27.728167 ms against a 60 ms budget. The host load average was 60-90 and
// single queries crossed their budgets on both this tree and its base d658ad5b
// in other runs, so the bench was compared base against head back to back.
//
// The same bench passed on 2026-11-13 for the 0113 bump; the worst measured
// query was search/fts/revealed at 50.56275 ms against a 60 ms budget, on a
// loaded shared host. 0113 adds no index or column on any benched table: the
// provenance table is read only by the media(id) detail read.
const schemaFreezeCatalogMigrations = 118
