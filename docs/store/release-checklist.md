# Release checklist: nightly freshness ceremony order

The release gate (`make release-gate`, which runs `backup-fixture-rc-check` and then
`script/release-gate-evidence.sh`) runs `script/nightly-freshness.sh check` as its third step. The
gate script runs under `set -euo pipefail`, so a failed check stops the gate before it writes the
release identity, the checksum list or the evidence archive. The check requires six records: fuzz-nightly, mutation-nightly, race-nightly,
ui-catalog-nightly, vuln-nightly and web-compat-nightly. Hosted CI does not produce them, so
`script/release-nightlies-local.sh` produces them on the release host first.

`check` accepts a record only when:

- its status is green;
- its `sourceCommit` is the checkout's current HEAD;
- its timestamp is no older than `NIGHTLY_FRESHNESS_MAX_HOURS` (default 24) at the moment the
  check runs, and no more than five minutes in the future.

All six recordings and the release gate's nightly-freshness step therefore have to happen on one
commit, inside one window.

## Ceremony order

1. **Check out the release commit with a clean worktree.** `script/release-nightlies-local.sh`
   refuses to start with modified or untracked non-ignored files. Make no commit and do not move
   HEAD from here until the release gate has finished; every record binds HEAD.
2. **Run `script/release-nightlies-local.sh`.** It runs, in this order, `make fuzz`,
   `script/mutation-test.sh named`, `script/race-test.sh full`, `make ui-catalog`,
   `make vuln-check` and `script/web-compat-nightly.sh`, recording each tier as it finishes. Each
   record's timestamp is taken when that tier is recorded; the first tier's record is the oldest
   and starts the window. A failed tier, or a mutation run whose status is not `COMPLETE`, is
   recorded red. web-compat-nightly's green record is written only by
   `script/web-compat-nightly.sh`. The runner ends by running `script/nightly-freshness.sh check`
   over all six tiers and exits 0 only when every tier is green and that check passes. Logs are
   kept under `.agent-evidence/release-nightlies-local/`.
3. **If a tier is red:** when the cause is outside the code (for example a missing tool), fix it
   and rerun only that tier with `script/release-nightlies-local.sh --tier NAME`; the closing
   check still covers all six. When the fix needs a commit, HEAD changes and every record goes
   stale, so return to step 1 and rerun all six tiers.
4. **Run `make release-gate` while the oldest record is still inside the window.** The window is
   measured when the gate reaches its nightly-freshness step, after `release-test-gates` and
   `clean-checkout-canary`, not when the gate starts.

If `RELEASE_NIGHTLY_EVIDENCE_DIR` is set to move the records away from
`.agent-evidence/nightly-last-green/`, set it to the same value for steps 2 to 4.
