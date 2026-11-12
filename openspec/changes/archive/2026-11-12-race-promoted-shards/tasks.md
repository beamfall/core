# Tasks

- [x] 1. Shard the promoted phase in `script/race-test.sh` and verify every listed test reports.
- [x] 2. Keep a failed shard failed in `race-short-packages.jsonl`.
- [x] 3. Extend `tools/gate/gate_scope_test.sh` for the split, the sticky failure and one shard.
- [x] 4. Time the sharded graph and store suites against the single-process run.
  Measured 2026-11-12 on the 12-core gate host, 4 shards per package, 6 at a time: 1314 s, all
  1335 top-level tests passing, against 4675 s (graph 1666 s + store 3009 s) in one process each.
