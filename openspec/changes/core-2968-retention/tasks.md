## 1. Retention correctness

- [x] Persist feed order and verify same-poll survivors.
- [x] Decide and trash each Media in one catalog write transaction against every referencing Subscription.
- [x] Cover pending stars, concurrency, recoverable Trash and actorless activity in focused tests.
