## 1. Lock scope (CORE-1348)

- [x] 1.1 `removeLocked` returns the session's teardown (stop the process, remove the directory, then release the lease and slot, which `stopping` keeps counted until then); TerminateSession, the idle reap, Close, mode-shaping replacement and preemption run it after unlocking
- [x] 1.2 `ensureRung` stops the replaced process, tears down a preempted victim and spawns the new process with the lock released, serialized per session by `startMu`; `startPending` keeps the slot counted
- [x] 1.3 Eviction while unlocked stops the spawned process, removes the directory and returns `ErrSessionEvicted`
- [x] 1.4 Tests with a slow fake Stop: teardown and replacement of one session do not block another session's request; a starting session still counts against the cap; eviction mid-replacement releases the lease and directory
- [x] 1.5 `Close` waits for off-lock starts (`starting`) and teardowns (`stopping`) so no encoder, lease or directory outlives it; a session with a start pending is not preemptable
- [x] 1.6 Tests: a torn-down session's slot and lease hold until its encoder stops; `Close` during a replacement returns only after the spawned encoder is stopped
- [x] 1.7 Tests: `Close` during another caller's teardown waits for it; a pending start is not preempted at the cap
