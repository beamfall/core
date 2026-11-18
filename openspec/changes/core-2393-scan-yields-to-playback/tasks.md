## 1. Governor

- [x] 1.1 `BeginPreemptible`: refused during transcodes, preempted by them, no budget slot
- [x] 1.2 Governor test for admission, preemption, budget independence and idempotent release

## 2. Jobs

- [x] 2.1 Shared `yieldsToPlayback` wrapper; `governedGeneration` uses it unchanged
- [x] 2.2 Scan job runs under `scanYieldsToPlayback` and logs each mid-run yield
- [x] 2.3 App test: preempted scan keeps its checkpoint and logs; refused scan never runs

## 3. Verification

- [ ] 3.1 Alien: Romulus plays on the Apple TV with library scans running (Tower deploy)
