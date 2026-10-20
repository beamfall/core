# Scale fixture generator

This is Beamfall's single large synthetic-library generator. Consumers select a tier and profile;
they must not fork the generation logic. Generated corpora and timing results are artifacts, never
committed blobs.

```sh
go run ./tools/scale-fixture \
  --tier 10k \
  --profile perf-realistic \
  --seed 171 \
  --output /tmp/beamfall-scale \
  --timings /tmp/beamfall-scale-timings.json
```

Tiers are `10k`, `100k`, and `1m`. Profiles are:

- `perf-realistic`: compact deterministic media-shaped files for throughput work.
- `messy-correctness`: adversarial Unicode/long/extensionless/deep names, varied file sizes, and
  deterministically broken payloads for correctness work.

The tuple `(VERSION, seed, tier, profile)` identifies a corpus. Every file path, byte, mode, and
mtime is deterministic, and `manifest.json` records a canonical corpus SHA-256. Generation refuses
to write into a non-empty directory so versions cannot be mixed.

Raw scan, filename-search, and thumbnail-probe durations are emitted as nanoseconds. They are
observations only; this tool owns no performance threshold. Re-measure a cached artifact without
regenerating it:

```sh
go run ./tools/scale-fixture \
  --tier 10k --profile perf-realistic --seed 171 \
  --output /tmp/beamfall-scale \
  --measure-only --timings /tmp/beamfall-scale-timings.json
```
