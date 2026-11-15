# Tasks (CORE-1763, CORE-1765, CORE-1766)

- [ ] 1. CORE-1763 conductor: join order, host hand-off on leave, `EndParty`, stream presence and
  expiry; Go tests
- [ ] 2. CORE-1763 sync and GraphQL: hand-off roster, `ended` signal, `leaveWatchParty(end:)`,
  event-stream observer and expiry timer; Go tests
- [ ] 3. CORE-1763 web: pagehide keepalive leave, hand-off and end notices, End for everyone;
  strings; vitest
- [ ] 4. CORE-1765 conductor and GraphQL: stored requests, `watch-party-request` notification,
  `request` signal, approve and decline on `watchPartyTransport`; Go tests
- [ ] 5. CORE-1765 web: relaxed or host-only start, Approve and Decline in the panel and on the
  notifications page; strings; vitest
- [ ] 6. CORE-1766 server: `reportDrift` for party members without re-anchoring; Go test
- [ ] 7. CORE-1766 web: rate-bounded drift report, member Up Next and autoplay defer to the party;
  vitest
- [ ] 8. `openspec validate --all --strict`; wsl preflight; `make gate` on the landing head
- [ ] 9. `openspec archive core-1763-watch-party-host-control` after landing, before completing
  the tickets
