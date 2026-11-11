## ADDED Requirements

### Requirement: Local producer for every nightly tier

The repository SHALL provide one local command that runs each of the six nightly freshness tiers
(fuzz-nightly, mutation-nightly, race-nightly, ui-catalog-nightly, vuln-nightly,
web-compat-nightly) against the current checkout and records each result through
`script/nightly-freshness.sh record`, then runs `script/nightly-freshness.sh check`.

#### Scenario: All tiers pass

- **WHEN** every tier's command exits 0 on a clean checkout
- **THEN** each tier has a green record bound to HEAD and `script/nightly-freshness.sh check`
  exits 0

#### Scenario: Provenance names the command

- **WHEN** the runner records a tier
- **THEN** the record's provenance names the local command and its exit status

### Requirement: A tier that did not pass is never green

The runner SHALL record a tier red when its command exits non-zero, and SHALL record
mutation-nightly red unless the mutation run reports `COMPLETE`. It SHALL NOT record a green for a
tier that did not run.

#### Scenario: Failed tier

- **WHEN** a tier's command exits non-zero
- **THEN** that tier's record has status red, the runner exits non-zero and `check` fails

#### Scenario: Time-boxed mutation run

- **WHEN** the mutation command exits 0 but reports `TIMEBOXED`
- **THEN** mutation-nightly is recorded red

### Requirement: Record-time forgery guards are kept

The runner SHALL leave the green web-compat-nightly record to `script/web-compat-nightly.sh`, and
`script/nightly-freshness.sh record` SHALL keep refusing a hand-written green for
web-compat-nightly and web-ui-e2e-smoke.

#### Scenario: Hand-written web-compat green

- **WHEN** `script/nightly-freshness.sh record --tier web-compat-nightly` is run with status green
- **THEN** it exits non-zero and writes no record

#### Scenario: Web-compat run publishes nothing

- **WHEN** the web-compat command exits 0 without publishing a record
- **THEN** the runner writes no green for it and `check` fails

### Requirement: Records bind the tested commit

The runner SHALL refuse to start on a worktree with uncommitted changes and SHALL stop without
recording further tiers if HEAD changes during the run.

#### Scenario: Dirty worktree

- **WHEN** the worktree has a modified or untracked non-ignored file
- **THEN** the runner exits non-zero before running any tier

#### Scenario: HEAD moves mid-run

- **WHEN** HEAD differs from the starting commit after a tier's command finishes
- **THEN** the runner records nothing for that tier and exits non-zero

### Requirement: Ceremony order is documented

`docs/store/release-checklist.md` SHALL list the release ceremony steps in order so that all six
records and the release gate fall inside one `NIGHTLY_FRESHNESS_MAX_HOURS` window on one commit.

#### Scenario: Operator follows the checklist

- **WHEN** an operator runs the steps in the listed order within the window
- **THEN** the release gate's nightly-freshness step finds six fresh green records for HEAD
