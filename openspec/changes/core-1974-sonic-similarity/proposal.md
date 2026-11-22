# Similar-Media retrieval over per-file feature vectors (sonic mixes, first slice)

Tickets: CORE-1974, CORE-3073, CORE-3074, CORE-3075 (`queue:core:main`); owner decisions CORE-3070.

## Why

"Sonically similar" rows and sonic mixes (post-1.0) need audio-feature vectors per file and a
"more like these" lookup seeded by Media. Feature extraction is DSP and belongs in Oscillux; core's
part is to store each file's vector and answer nearest-neighbour reads, staying domain-agnostic,
opt-in and local-only. The ADR-0041 vector store already keeps opaque per-fingerprint vectors of
any kind, but its only reads are a text-query search (`SemanticSearch`) and an engagement-seeded
recommendation; neither takes seed Media, and both carry two defects that matter for centred
feature vectors: a cosine of exactly -1 collided with the dimension-mismatch sentinel and was
dropped, and candidates were ranked with a quadratic insertion sort.

## What changes

- New store read `SimilarMedia(filter, kind, seeds, limit)`: centroid of the seeds' unit-length
  vectors, cosine nearest neighbours, seeds never listed, Reveal and certification scoping for both
  seeds and candidates, scan bounded by a value budget (4M values, at least 1,000 rows).
- The vector scan compares lengths instead of a sentinel score, so an exact opposite scores -1 and
  is kept; candidates are ranked by a stable O(n log n) sort.
- A JSON test fixture (`internal/store/testdata/similar_media_fixture.json`) pins ordering, scoping
  and empty-result cases for any kind.
- `design.md` records the cross-repo design (Oscillux sonic profile, server runtime, analysis job,
  graph and web surfaces), cost and storage estimates, the owner decisions and the ticket map.

- Per-Library sonic-analysis toggle, default OFF, stored as a `server_config` row (no migration),
  with an Admin graph read and mutation (CORE-3073).
- An idle-only background job decodes a 60 s excerpt with FFmpeg and stores the vector a
  `SonicAnalyzer` returns. Until the Oscillux WASI artifact and its wazero loader exist
  (CORE-3072), no analyser is wired and the job does nothing (CORE-3073).
- Graph read `similarMedia(mediaID, limit)` and Station seed kind `sonic` (CORE-3074), an additive
  client-core-wire change.
- Web "Sonically similar" row on audio Media detail, hidden while empty (CORE-3075).

No migration.

## Non-goals

- Audio analysis in core. Oscillux owns extraction (CORE-3071); the wazero loader for its
  artifact is CORE-3072.
- "Start sonic mix" and a Console toggle in the web UI.
- Indexed nearest-neighbour search past the scan budget (CORE-3076).
- Telemetry or cloud analysis of any kind.
