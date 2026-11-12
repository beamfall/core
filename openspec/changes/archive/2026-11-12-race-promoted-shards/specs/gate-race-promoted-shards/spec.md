## ADDED Requirements

### Requirement: Promoted suites run in concurrent shards

The race-short step SHALL run each promoted package's full `-race -short` suite as concurrent
shards that together name every top-level test the package lists exactly once.

#### Scenario: Promoted package with several tests

- **WHEN** a diff directly changes a promoted package that lists more than one top-level test
- **THEN** the tests are dealt into at most `RACE_SHORT_PROMOTED_SHARDS` shards, each shard runs as
  its own `go test -race -short -count=1` process, and no more than `RACE_SHORT_PROMOTED_JOBS` run
  at a time

#### Scenario: One shard or an unlistable package

- **WHEN** the shard count is one, the package lists fewer than two tests, or listing fails
- **THEN** the package runs as a single `go test` process with no `-run` filter

### Requirement: Sharding never weakens the suite

The race-short step SHALL fail when a listed test of a sharded package reports no result, and SHALL
record a package as failed when any of its shards failed.

#### Scenario: A listed test does not report

- **WHEN** a shard exits successfully but a test assigned to it has no pass, fail or skip result
- **THEN** the step names the test and exits non-zero

#### Scenario: One shard fails and a later one passes

- **WHEN** one shard of a package fails and another shard of the same package passes
- **THEN** every shard still runs, the step exits non-zero and `race-short-packages.jsonl` records
  the package with status `fail`
