# Tasks

## 1. Cross-mode step reuse (CORE-1569)

- [x] 1.1 Drop the preflight mode and the coordinator-entry variables from the reuse key; add the
      mode-bound predicate
- [x] 1.2 Add the `source` and `source+index` classes and put the six steps on the list with their
      soundness arguments; list the steps that stay unreusable with the reason
- [x] 1.3 Record the producing run's kind and show it in the `REUSE` line and evidence row
- [x] 1.4 `tools/gate/gate_step_reuse_test.sh`: preflight pass reused by the full gate and the
      reverse, a different command across modes runs, a mode-bound step is not reused across modes,
      the new classes ignore the bundle and follow the index
- [x] 1.5 Update `docs/architecture/TESTING.md`

## 2. Machine-wide gate slot (CORE-1470)

- [ ] 2.1 `script/gate-slot.py`: first-come queue, holder and position notices, `--no-wait`,
      `--status`, release on exit and on INT/TERM/HUP, dead-process reclaim, re-entry
- [ ] 2.2 Core coordinator takes the slot for a full gate and announces the holder to a preflight
- [ ] 2.3 `script/gate_slot_test.py` in `support script tests`: two gates serialize in arrival
      order, a stale entry is reclaimed, refusal, cancellation, re-entry, coordinator integration
- [ ] 2.4 Document the slot in `docs/architecture/TESTING.md`

## 3. Verify and close

- [ ] 3.1 Changed `tools/gate/*_test.sh`, `gate_latest_test.py`, `gate_capacity_test.py`,
      `openspec validate --all --strict`
- [ ] 3.2 wsl preflight, then one Mac preflight and the binding gate on the same head; record the
      before and after seconds on CORE-1569
- [ ] 3.3 File the adoption tickets for the other repositories
