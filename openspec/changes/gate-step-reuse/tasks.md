## 1. Build

- [x] 1.1 Add the reuse key, lookup and record functions to `tools/gate/gate.sh`
- [x] 1.2 Consult and record them in `run_step` for the listed steps
- [x] 1.3 Add `tools/gate/gate_step_reuse_test.sh` and run it with the support tests
- [x] 1.4 Describe reuse in `docs/architecture/TESTING.md`

## 2. Prove

- [x] 2.1 Full gate passes on the lane (head eee7fd0, 2026-11-10)
- [x] 2.2 A second gate on the same tree reuses the listed steps; record both wall times (781 s with no reuse on the parent head; 546 s with six steps reused from an earlier partial run; 385 s with all ten listed steps reused)

## 3. Close

- [ ] 3.1 Archive this change after landing, before completing the ticket
