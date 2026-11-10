## 1. Inheritance

- [x] 1.1 Keep an inherited `BEAMFALL_GATE_CAPACITY_FD` open across `script/gate-fixture-guard.py`
  and drop a stale name
- [x] 1.2 Adopt an owning inherited descriptor in `tools/gate/gate_latest.py`; refuse a non-owning
  one with exit 70; ignore a foreign or closed one

## 2. Tests

- [x] 2.1 Fixture guard: inherited descriptor stays open; stale descriptor is dropped
  (`script/gate_fixture_guard_test.py`)
- [x] 2.2 Coordinator: adopts, refuses and ignores inherited descriptors
  (`tools/gate/gate_latest_test.py`)
- [x] 2.3 Real evidence-wrapper -> make gate -> gate-entry -> guard -> coordinator routing with a
  fake gate body: starts once and completes, queues behind a capacity holder, direct gate takes
  capacity, queued and running interruptions are descendant-clean

## 3. Verify and close

- [ ] 3.1 Run `make gate-preflight` and `make gate`
- [ ] 3.2 Run `openspec archive gate-capacity-inheritance` after landing, before completing CORE-0392
