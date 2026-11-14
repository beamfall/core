# Tasks (CORE-1170)

- [x] 1. Keep the failure count until a watch has stayed up for the stability window
- [x] 2. Rate-limit the degrade warning and the overflow line per root; count what was suppressed
- [x] 3. Tests: flapping backs off to the cap, a stable watch resets, warnings are rate limited
- [x] 4. `make gate` at landing; `openspec archive watcher-flap-backoff`
