## 1. Coordinator

- [x] 1.1 Preflight takes `beamfall-gate.preflight.lock` instead of gate capacity and keeps its
  state under `preflight/`; its body gets no capacity descriptor and `BEAMFALL_GATE_CAPACITY_HELD=1`
- [x] 1.2 Record the holder in each lock the coordinator takes; announce waits immediately and
  every 30 seconds

## 2. Tests

- [x] 2.1 Preflight completes while an external holder owns capacity
  (`tools/gate/gate_latest_test.py`)
- [x] 2.2 A preflight queued behind another worktree's preflight announces the holder
- [x] 2.3 A full gate queued on capacity announces the holder
- [x] 2.4 The evidence-wrapped routing tests run the full-gate path with stubbed prerequisites
  instead of borrowing `GATE_PREFLIGHT`

## 3. Verify and close

- [x] 3.1 Focused: `python3 tools/gate/gate_latest_test.py`
- [ ] 3.2 Receipted `make gate` on the landing head
