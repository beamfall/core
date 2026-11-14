# Tasks (CORE-1677)

- [x] 1. Conductor: paused party state; anchor holds while paused; Go tests
- [x] 2. Sync channel: per-member-Profile fan-out; `paused`; `roster` signal on create/accept/leave; Go tests
- [x] 3. GraphQL: optional `paused` on `watchPartyTransport` and `requestWatchPartyTransport`
- [x] 4. CORE-0531: create and accept refuse non-home-network callers; Go tests
- [x] 5. Web: start party, invite by Profile, members panel, leave, join from notification
- [x] 6. Web: group.sync playback sync, local drift correction, drift indicator; vitest
- [x] 7. Strings in every shipped locale
- [ ] 8. `openspec validate --all --strict`; wsl preflight; `make gate` on the landing head
- [ ] 9. `openspec archive core-1677-watch-party-web` after landing, before completing the ticket
