# Tasks (CORE-2282)

- [ ] 1. Bound the TCP carrier socket's unsent backlog below TLS on Linux and macOS; a test on
  each platform proves the bound is in effect on a dialled carrier
- [ ] 2. Measure control-stream latency with this client as the media sender and record it in
  beamfall-relay `docs/relay-latency-budget.md`; record Windows as unbounded with a follow-up ticket
