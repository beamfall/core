# Run the promoted race-short suites across the host's cores

Ticket: CORE-1304 (`queue:core:main`). Related: CORE-0980 (long gate steps run one after another),
CORE-0983 (race-scope inputs), CORE-0566 (store tests replay every migration).
Status: proposed.

## Why

A diff that directly changes `internal/graph` or `internal/store` promotes that package to its full
`-race -short` suite. The promoted phase ran both packages in one `go test -p 1` process. Their
tests are mostly serial, so the phase used one core of a twelve-core host: about 28 minutes for
graph and more than 30 for store on CORE-1190, and a gate of over 80 minutes.

## What Changes

- `script/race-test.sh short` lists each promoted package's top-level tests, deals them into
  shards and runs the shards as concurrent `go test -run` processes with the same race flags.
- The phase fails when a listed test reports no result, so a shard cannot drop a test silently.
- `race-short-packages.jsonl` keeps a package failed when any of its shards failed.
- `RACE_SHORT_PROMOTED_SHARDS` (default 4) sets the shards per package and
  `RACE_SHORT_PROMOTED_JOBS` (default half the host's cores, between 2 and 8) the concurrency.
  One shard, or a package whose tests cannot be listed, runs the previous single command.
- `tools/gate/gate_scope_test.sh` proves the split, the sticky failure and the unsharded path.

## Non-goals

- Changing which packages are promoted, or the ordinary and focused phases.
- Making the store tests cheaper (CORE-0566) or running whole gate steps side by side (CORE-0980).
