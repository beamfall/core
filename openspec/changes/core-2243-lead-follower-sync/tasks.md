# Tasks (CORE-2243, CORE-2244, CORE-2245)

- [x] 1. CORE-2243 conductor: lead party, lead report re-anchor, heartbeat, rate, kept invites,
  end on lead leave; Go tests including the 2 s stall
- [x] 2. CORE-2243 sync and GraphQL: `lead` and `ratePermille` payload fields,
  `createWatchParty(lead:)`, lead report on `reportDrift`, follower transport refused, `ended` on
  lead leave and expiry; Go tests
- [ ] 3. CORE-2244 calibration: fractional-millisecond exchange, lowest-round-trip estimate with
  re-estimation; Go test and vitest
- [ ] 4. CORE-2245 web: lead start, lead reports, muted followers at the timeline rate, 100 ms
  loop, display delay trim, rejoin; strings; vitest
