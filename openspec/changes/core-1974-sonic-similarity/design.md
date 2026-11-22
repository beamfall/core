# Design: sonic mixes and sonically similar Media

Status: proposed (post-1.0). Ticket: CORE-1974. Owner decisions: CORE-3070.

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

1. `Store.SimilarMedia` (this change): centroid of unit seed vectors, cosine scan over the
   Reveal-eligible set, seeds excluded, at most 4M values scored (125,000 Media at 32 dims).
2. Analysis job (CORE-3073): FFmpeg decodes to PCM, the pinned Oscillux artifact returns the vector,
   `UpsertEmbedding(fingerprint, "sonic-v1", v)`. Per-library toggle in a new appended migration,
   local-only, no telemetry, yields to playback (`background-job-playback-yield`), idle scheduling,
   retry cap.
3. Graph (CORE-3074): generic `similarMedia(seeds, limit)` read and a sonic seed mode for Stations,
   with a client-core-wire bump.
4. Web (CORE-3075): "Sonically similar" row on audio Media detail and "Start sonic mix".

## Cost and storage (estimates, not measured)

- Storage: 32 float32 is 128 B per file, about 10 MB per 50,000 files plus row overhead.
- Analysis: dominated by decode. Roughly 1 s of CPU per track including FFmpeg decode, so about
  14 CPU-hours for 50,000 tracks; an excerpt (for example 90 s from the middle) cuts that by about
  half for typical tracks at some loss on tracks that change character.
- Query: brute force over 125,000 x 32 values is a few milliseconds of arithmetic plus the SQLite
  read of about 16 MB; larger libraries need CORE-3076.

## Owner decisions (CORE-3070)

- **D1 Server runtime.** Core is pure Go and has no Oscillux runtime since CORE-1506 removed the
  beat-grid analyser (Oscillux is AGPL-3.0-only since ADR-0233, so the licence block is gone).
  Options: WASI module via wazero (pure Go, deterministic, new dependency), CGO link of
  `oscillux-ffi` (breaks the pure-Go build), bundled CLI subprocess (per-platform binaries), or
  client-side analysis uploaded to the server. Recommendation: WASI via wazero, else a CLI.
- **D2 Analysis cost.** Whole track or excerpt; idle-only or continuous with playback yield; the
  estimate above is unmeasured and needs a benchmark on the owner's hardware.
- **D3 Opt-in default.** `ai_indexing_enabled` defaults ON. Recommendation: a separate per-library
  audio-analysis toggle defaulting OFF.
- **D4 Model.** v1 is hand-engineered DSP with KB-sized normalisation constants; which reference
  corpus derives those constants is open. A learned embedding (CNN or CLAP style) would ship tens of
  MB of weights and about 2 KB per vector, with licence and provenance questions for the weights.
- **D5 Storage and width.** 32 dims is negligible; wider vectors shrink scan coverage under the
  value budget and raise storage linearly.

## Ticket map

| Ticket | Work | Depends on |
|---|---|---|
| CORE-1974 | Store read, fixture, design (this change) | none |
| CORE-3070 | Owner decisions D1-D5 | none |
| CORE-3071 | Oscillux sonic profile and sonic-v1 vector | CORE-3070 |
| CORE-3072 | Oscillux server-runnable artifact | CORE-3070, CORE-3071 |
| CORE-3073 | Core opt-in analysis job | CORE-3070, CORE-3072, CORE-1974 |
| CORE-3074 | Graph read and sonic Station mode | CORE-1974, CORE-3073 |
| CORE-3075 | Web row and Start sonic mix | CORE-3074 |
| CORE-3076 | Indexed nearest-neighbour search | CORE-1974 |
| CORE-3077 | RecommendFromEngagement nondeterministic dims (bug) | none |
