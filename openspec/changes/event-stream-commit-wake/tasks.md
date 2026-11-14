# Tasks (CORE-1158)

- [x] 1. Commit signal in the store (`Store.EventOutboxChanged`), raised by every outbox write
      path after its commit; store tests
- [x] 2. Event stream waits on the signal with timed keepalive, Scope re-check and a 30 s fallback
      read; server tests for idle cost, delivery on the signal and the fallback
- [ ] 3. `make gate` at landing; `openspec archive event-stream-commit-wake`
