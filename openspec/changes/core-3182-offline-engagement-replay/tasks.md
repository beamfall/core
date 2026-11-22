# Tasks (CORE-3182)

- [x] 1. Events migration 0028: offline engagement ledger; frozen schema and backup fixture (PR restore drill green, no new fixture needed)
- [x] 2. Store: apply one offline event in one transaction (ledger, merge rules, projection event)
- [x] 3. Profile erasure and per-Media deletion clear the ledger
- [x] 4. Graph: `replayOfflineEngagement` mutation, ordering, bound, invalidation
- [ ] 5. Wire contract and mgmt manifests regenerated
- [x] 6. Unit tests: ordering, idempotency, Profile scoping, stale position, sticky completion
