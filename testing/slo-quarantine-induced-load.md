# Induced-load procedure: playback SLO quarantine (SLO_INCONCLUSIVE)

Ticket: `GATE-TRANSCODE-SLO-LOAD-FALSE-RED-1`.

The real-encoder wall-clock SLO in `internal/playback/sessions_test.go`
(`TestTranscodeLatencyBudgetsOnReferenceClip`, ADR-0155 Decision 2.2/2.3) is
measured against absolute budgets that ambient host contention can blow without
any transcode regression. The quarantine converts exactly that case into an
explicit, logged `SLO_INCONCLUSIVE` skip — never a silent pass, and never a
skip for the deterministic injected regression.

This file is the repeatable procedure for proving both directions on a real
host. It is a manual procedure on purpose: the assertion cannot self-verify
that a red became a skip without actually oversubscribing the machine, and
sustained synthetic load must never run inside `make gate` (it would be the
false-red generator this ticket exists to eliminate).

## What is proved without induced load

The policy itself is pure and table-proved — no host state required:

- `sloQuarantineDecision(injected, loadBefore, loadAtFailure, ok, numCPU) bool`
  — `TestSLOQuarantineDecision`: the injected path never quarantines at any
  load; a non-injected overrun quarantines only when BOTH samples exceed
  `NumCPU`; `load == NumCPU` does not quarantine; `ok=false` (host load
  unreadable) fails closed to a red.
- `parseLoadAverage1(uptimeOutput) (float64, bool)` — `TestHostLoadAverageParsing`:
  macOS and Linux `uptime` formats, plus empty/garbage/non-numeric output
  classifying as `ok=false`.

Run them with:

```sh
go test ./internal/playback -run 'TestSLOQuarantineDecision|TestHostLoad|TestTranscodeLatencyBudgetClassification' -count=1
```

The induced-load procedure below proves the remaining claim: that the wired
call sites actually reach that decision on a real oversubscribed host.

## Two-sample fence (why one sample is not enough)

`load1` is a **one-minute average**, so a single sample taken at the moment of
failure is stale by construction. `tools/gate/gate.sh` documents its own 8-way
parallel batch peaking near `load1≈96` on a 12-core host, and deliberately runs
the playback step *after* that batch drains — precisely the window in which
`load1` still reads far above `NumCPU` on a host that is already quiet. A
one-sample quarantine would skip a genuine regression there.

The quarantine therefore samples host load **before the timed operation starts**
as well as at the failure, and fires only when both readings exceed `NumCPU`
(sustained oversubscription spanning the measurement). The residual window is
now bounded by the duration of one timed operation rather than by the full
one-minute averaging window.

## Procedure

Host facts needed: `sysctl -n hw.logicalcpu` (macOS) or `nproc` (Linux) — call
it `N`. On the reference host used for this ticket, `N = 12`.

### A. Loaded direction — a wall-clock overrun must become a skip, not a red

1. Confirm the starting load: `uptime`.
2. Spin more CPU-burn workers than cores, and leave them running for at least
   90 seconds before step 3 so the one-minute average actually rises and both
   samples will see it:

   ```sh
   for i in $(seq 1 $(( $(sysctl -n hw.logicalcpu) * 4 ))); do yes > /dev/null & done
   sleep 90; uptime   # confirm load1 is well above N
   ```

3. With that load sustained, run the SLO lane exactly as the gate runs it
   (`tools/gate/gate.sh:804`):

   ```sh
   env BEAMFALL_PERF=1 BEAMFALL_PERF_LOAD_WORKERS=1 GOMAXPROCS=2 \
     go test ./internal/playback -run '^TestTranscodeLatencyBudgetsOnReferenceClip$' \
     -v -count=1 -timeout 90s
   ```

4. Stop the workers immediately: `kill %1 %2 ... ` or `pkill -f '^yes$'`.

Expected outcome: the package reports `ok` and the test line is a **SKIP**
carrying `SLO_INCONCLUSIVE reason=host_contention` with `load1_before`,
`load1_at_failure`, and `logical_cpus` — not a `FAIL`. If the host is fast
enough that the budget is not blown even under that load, the test simply
PASSes; that is not a failure of the quarantine, it means the overrun the
quarantine exists for did not occur. Increase the worker count and repeat.

### B. Quiet direction — the same command must still pass unquarantined

On an idle host (`load1` below `N`, no gate lanes running), the identical
command must PASS with no `SLO_INCONCLUSIVE` anywhere in the output.

### C. Genuine-regression direction — injection must red at any load

The injected path is proved in-gate and needs no manual step:
`TestInjectedPlaybackLatencyRegressionReds` runs the inner test with
`BEAMFALL_PLAYBACK_LATENCY_INJECT=9s` and `t.Fatal`s if the inner output
contains `SLO_INCONCLUSIVE` at all. Run it under induced load (step A's
workers) to confirm the bypass holds on a genuinely oversubscribed host:

```sh
env BEAMFALL_PERF=1 BEAMFALL_PERF_LOAD_WORKERS=1 GOMAXPROCS=2 \
  go test ./internal/playback -run '^TestInjectedPlaybackLatencyRegressionReds$' \
  -v -count=1 -timeout 90s
```

Expected outcome: PASS (the inner run red, and did so for a genuine reason).

## Recorded evidence

### Ticket-recorded, unmodified `main` at `02e5b4120` (pre-fix false red)

The identical focused command passed standalone at `load1 = 136`, and failed
with the wall-clock SLO blown at `load1` in the range `165–284`. Same binary,
same fixture, same budgets — the only variable was host load. That is the
false red this quarantine converts to `SLO_INCONCLUSIVE`.

### Quiet-ish direction, this repair pass (branch `run/GATE-TRANSCODE-SLO-LOAD-FALSE-RED-1`)

Host: 12 logical CPUs, live agent fleet running (load1 above `N`, but far below
the failing range, so the budget was not blown and the quarantine was never
reached):

```
PRE: 13:56  up 7 days,  5:33, 6 users, load averages: 25.47 19.28 22.73
$ env BEAMFALL_PERF=1 BEAMFALL_PERF_LOAD_WORKERS=1 GOMAXPROCS=2 go test ./internal/playback -run '^TestTranscodeLatencyBudgetsOnReferenceClip$' -v -count=1 -timeout 90s
=== RUN   TestTranscodeLatencyBudgetsOnReferenceClip
    sessions_test.go:547: controlled_load_start workers=1 host_load="13:56  up 7 days,  5:33, 6 users, load averages: 25.47 19.28 22.73"
    sessions_test.go:549: reference_resources duration_s=48 input=64x64@1fps output=64p gomaxprocs=2 logical_cpus=12 injected_latency=0s host_load="13:56  up 7 days,  5:33, 6 users, load averages: 25.47 19.28 22.73"
    sessions_test.go:809: controlled_load_stop workers=1 operations=7462200000 elapsed=3.75611725s host_load="13:56  up 7 days,  5:33, 6 users, load averages: 24.55 19.20 22.68"
--- PASS: TestTranscodeLatencyBudgetsOnReferenceClip (3.77s)
PASS
ok  	github.com/beamfall/core/internal/playback	4.051s
POST: 13:56  up 7 days,  5:33, 6 users, load averages: 24.55 19.20 22.68
```

No `SLO_INCONCLUSIVE` in the output: an unblown budget never reaches the
quarantine at all.

Step A (sustained induced load) was **not** re-run during this repair pass: the
host was running concurrent `make gate` lanes, and adding sustained
oversubscription would have injected false reds into unrelated agents' gates.
The loaded direction was captured earlier in this ticket on the same host and
is recorded in `.agent-evidence/GATE-TRANSCODE-SLO-LOAD-FALSE-RED-1.txt`. Re-run
step A on a quiesced host when the quarantine policy changes again.
