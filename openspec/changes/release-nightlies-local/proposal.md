# Give the release gate a local producer for the six nightly freshness tiers

Ticket: CORE-0539 (`queue:core:main`). ADR: 0013.

## Why

`script/release-gate-evidence.sh` runs `script/nightly-freshness.sh check` as a step under
`set -euo pipefail`, so a failed check aborts the release gate before it produces any artifact. The
check needs a fresh record for six tiers: fuzz-nightly, mutation-nightly, race-nightly,
ui-catalog-nightly, vuln-nightly and web-compat-nightly. The only producers of those records are
the hosted nightly workflows, and hosted CI is disabled, so the records have never been written and
the release gate cannot pass.

## What Changes

- Add `script/release-nightlies-local.sh`. It runs each tier's existing local command against the
  current checkout and records the result through `script/nightly-freshness.sh record`, with
  provenance that names the command and its exit status:
  - fuzz-nightly: `make fuzz`
  - mutation-nightly: `script/mutation-test.sh named` (green only when its status file says
    `COMPLETE`, as in the hosted workflow)
  - race-nightly: `script/race-test.sh full`
  - ui-catalog-nightly: `make ui-catalog`
  - vuln-nightly: `make vuln-check`
  - web-compat-nightly: `script/web-compat-nightly.sh`, which publishes its own green record
- A tier whose command fails is recorded red. The runner never records a green for a tier that did
  not run or did not pass, and it never records web-compat-nightly green itself.
- The runner refuses to start on a dirty worktree, stops recording if HEAD moves during the run,
  and finishes by running `script/nightly-freshness.sh check` for all six tiers.
- Add `script/release-nightlies-local_test.sh` (fakes only) and run it from
  `script/nightly-freshness_test.sh`, which the gate already runs.
- Add `docs/store/release-checklist.md` with the ceremony order that keeps all six records and the
  release gate inside one freshness window on one commit.

## Non-goals

- Changing `script/nightly-freshness.sh check`, its 24-hour window or the two record-time guards
  that refuse a hand-written green for web-compat-nightly and web-ui-e2e-smoke.
- Changing or re-enabling the hosted nightly workflows.
- Changing what each tier's command tests.

## Failure modes

- A tier fails locally: it is recorded red, the runner exits non-zero and `check` fails.
- mutation-nightly is cut short by its time box (exit 0, status `TIMEBOXED`): recorded red.
- web-compat-nightly fails: recorded red through `record --status red`, which the guard allows.
- The worktree is dirty, or HEAD changes mid-run: the runner exits non-zero; no record binds a
  commit that was not the one tested.
- The recordings or the gate fall outside one 24-hour window: `check` fails as before.

## Acceptance evidence

- `script/release-nightlies-local_test.sh` proves that with passing fake tiers `check` exits 0
  after the runner, that a failed or time-boxed tier is recorded red and never green, that a
  web-compat run which publishes nothing is not turned into a green, and that a dirty tree is
  refused.
- `script/nightly-freshness_test.sh` still proves the web-compat-nightly green guard refuses a
  hand-written record.
- `make gate` passes.

## Rollback

Delete the runner, its test, the call from `script/nightly-freshness_test.sh` and the checklist.
Records already written are ordinary nightly-freshness records and expire after the window.

## Capabilities

### New Capabilities

- `release-nightly-evidence`: how the six nightly freshness records are produced locally for a
  release.

### Modified Capabilities

None.
