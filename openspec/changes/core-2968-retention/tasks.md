## 1. Retention correctness

- [x] Persist feed order and verify same-poll survivors.
- [x] Recheck both policies and all linked delete-after-played Subscriptions at the Trash write.
- [x] Cover pending stars, concurrency, recoverable Trash and actorless activity in focused tests.
