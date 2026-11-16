## 1. Server

- [x] 1.1 Store primitive that runs a write only while a session stays bound to a Profile,
      serialized against Profile rebinds
- [x] 1.2 `expectedProfileID` on `queueAdd`, `queueRemove`, `queueMove`, `queueClear` and
      `stationStart`; mismatch answers `CONFLICT` / `PROFILE_MISMATCH` and writes nothing
- [x] 1.3 Tests: stale precondition refused with the new Profile's queue untouched, matching
      precondition applies, absent precondition unchanged, concurrent `switchProfile` never puts
      the item in the new Profile's queue (under -race)
- [x] 1.4 A supplied blank `expectedProfileID` is refused as `INVALID` on all five mutations;
      `stationStart` checks the precondition before resolving its seed

## 2. Wire contract

- [x] 2.1 Wire goldens regenerated
- [x] 2.2 `client-core-wire` 2.15.0 published and pinned in `contracts/registry.json`
