# Design: sonic mixes and sonically similar Media

Status: accepted (post-1.0). Ticket: CORE-1974. Owner decisions: CORE-3070, accepted 2026-11-21 as
recommended (D1 wazero and WASI, D2 idle-only excerpt, D3 separate toggle default OFF, D4 DSP
features only, D5 storage accepted).

## Split of responsibilities

| Concern | Owner |
|---|---|
| Audio feature extraction (tempo, key, loudness, spectral and timbre summary), the vector layout and its version | Oscillux (`sonic-profile` capability, OpenSpec change `add-sonic-profile`) |
| Running the pinned extractor on the server, per-library opt-in, scheduling, storage | Core (`internal/genassets`-style job, ADR-0041 `media_embedding`) |
| Nearest-neighbour reads, scoping, mixes (Stations) | Core (`Store.SimilarMedia`, graph) |
| "Sonically similar" row and "Start sonic mix" | Web and clients, reading the graph |

Core never interprets the vector: it is an opaque `media_embedding` row of kind `sonic-v1` keyed by
content fingerprint, like any other kind. No domain words enter core; "sonic" names only the kind
string and the job.

## Oscillux API needed

Oscillux v0.2.11 already has offline whole-file beat-grid analysis (global and local tempo, per-frame
12-bin chroma, meter) and per-block descriptors (BS.1770 momentary and short-term loudness, width,
balance, spectral centroid and flux). Missing:

1. Key and mode estimation from accumulated chroma (key-profile correlation).
2. Integrated loudness and loudness range over a whole file (BS.1770 gating).
3. A timbre summary: about 20 log-mel band means (and optionally spread).
4. Onset rate and pulse clarity as whole-file scalars.
5. `sonic_profile(pcm, sample_rate, channels) -> SonicProfile` returning named features, and
   `sonic_vector_v1(&SonicProfile) -> [f32; N]` packing them into a fixed-length, centred, signed
   vector with a version tag. Exposed through `oscillux-ffi` and the wasm package, with conformance
   vectors for each feature and for the packed vector, deterministic across platforms.
6. A server-runnable artifact core can pin without CGO (owner decision D1): a WASI module run by
   wazero, or a small CLI reading PCM on stdin and writing the vector on stdout, listed with
   checksums in the release manifest.

### sonic-v1 vector (proposed, about 32 float32)

Each component is z-scored with fixed population constants shipped in Oscillux (a few KB), so the
vector is centred and cosine similarity spans [-1, 1].

- Tempo: octave-folded log2 tempo as (cos, sin), scaled by tempo confidence (2).
- Key: circle-of-fifths angle, relative major and minor sharing an angle, as (cos, sin) scaled by
  key strength, plus mode (3).
- Integrated loudness, loudness range (2).
- Log spectral centroid, spectral spread, flux, onset rate, stereo width, pulse clarity (6).
- Log-mel band means, about 19 bands (19).

Weights per group keep tempo and key from dominating timbre. The layout is versioned: a new layout is
a new kind (`sonic-v2`), so old vectors are never compared with new ones and reanalysis is explicit.

## Core pieces

1. `Store.SimilarMedia` (CORE-1974): centroid of unit seed vectors, cosine scan over the
   Reveal-eligible set, seeds excluded, at most 4M values scored (125,000 Media at 32 dims).
2. Analysis job (CORE-3073, built): the `sonic-analysis-sweep` background job walks audio-class
   Media without a vector of the analyser's kind in Libraries whose toggle is on, in Media ID order
   with a checkpoint, five items per slice. FFmpeg decodes a 60 s excerpt from the middle of the file
   at 22.05 kHz stereo into memory (`Runner.AudioExcerpt`), a `SonicAnalyzer` returns the vector, and
   `UpsertEmbedding(fingerprint, kind, v)` stores it. Vectors that are empty, wider than 256 values,
   non-finite or all zero are refused. Sealed and Encrypted Libraries are skipped. The job is
   registered through the resource governor, so it runs only while no playback transcode or scan
   is active and yields when one starts (D2). A failure is counted and retried by the next sweep.
3. Toggle (CORE-3073, built): a per-Library `server_config` row `sonicanalysis.library.<id>`; an
   absent row is OFF (D3), so no migration is needed. It is separate from `ai_indexing_enabled`.
   Admin graph read `sonicAnalysisEnabled` and mutation `setSonicAnalysisEnabled`; enabling is
   refused while no analyser is wired, and enabling arms the sweep. Turning it off keeps vectors.
4. Analyser seam (CORE-3072): `genassets.SonicAnalyzer` is the interface the wazero loader will
   implement over the pinned Oscillux WASI artifact. No artifact exists yet, so the composition root
   wires none: the toggle reports `available: false` and the sweep is inert. Tests use a double.
5. Graph (CORE-3074, built): `similarMedia(mediaID, limit)` (default 20, max 50) reads
   `SimilarMedia` with the `sonic-v1` kind through the caller's scope, with passive-rail Mute
   suppression like `relatedMedia`. Station seed kind `sonic` takes a Media id and keeps similarity
   order (the seed, then nearest first; tuning bias only, no shuffle). This is a client-core-wire
   minor bump that the landing owner publishes.
6. Web (CORE-3075, built): a "Sonically similar" row on audio Media detail pages, hidden while
   empty; non-audio Media never send the read. "Start sonic mix" waits for a client entry point.

## Cost and storage (estimates, not measured)

- Storage: 32 float32 is 128 B per file, about 10 MB per 50,000 files plus row overhead.
- Analysis: dominated by decode. Roughly 1 s of CPU per track including FFmpeg decode, so about
  14 CPU-hours for 50,000 tracks; an excerpt (for example 90 s from the middle) cuts that by about
  half for typical tracks at some loss on tracks that change character.
- Query: brute force over 125,000 x 32 values is a few milliseconds of arithmetic plus the SQLite
  read of about 16 MB; larger libraries need CORE-3076.

## Owner decisions (CORE-3070), accepted 2026-11-21

- **D1 Server runtime: WASI via wazero.** Core is pure Go and has no Oscillux runtime since
  CORE-1506 removed the beat-grid analyser (Oscillux is AGPL-3.0-only since ADR-0233). The pinned
  Oscillux sonic-profile WASI module runs in wazero (pure Go, deterministic, no CGO). Rejected: CGO
  link of `oscillux-ffi`, a bundled CLI per platform, client-side analysis.
- **D2 Analysis cost: idle-only, excerpt.** One 60 s window from the middle of each file, analysed
  only while the server is idle (resource-governor admission and preemption). The cost estimate
  above is still unmeasured; CORE-3072 benchmarks it once the artifact exists.
- **D3 Opt-in: a separate per-Library toggle, default OFF.** Independent of `ai_indexing_enabled`.
- **D4 Model: DSP features only.** Hand-engineered `sonic-v1` features with KB-sized normalisation
  constants; no learned embedding and no shipped weights.
- **D5 Storage: accepted.** About 128 B per file at 32 dims.

## Ticket map

| Ticket | Work | Depends on |
|---|---|---|
| CORE-1974 | Store read, fixture, design (this change) | none |
| CORE-3070 | Owner decisions D1-D5 | none |
| CORE-3071 | Oscillux sonic profile and sonic-v1 vector | CORE-3070 |
| CORE-3072 | Oscillux server-runnable artifact | CORE-3070, CORE-3071 |
| CORE-3073 | Core opt-in analysis job | CORE-3070, CORE-3072, CORE-1974 |
| CORE-3074 | Graph read and sonic Station mode | CORE-1974, CORE-3073 |
| CORE-3075 | Web row (built); Start sonic mix | CORE-3074 |
| CORE-3076 | Indexed nearest-neighbour search | CORE-1974 |
| CORE-3077 | RecommendFromEngagement nondeterministic dims (bug) | none |
