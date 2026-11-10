## 1. Server

- [ ] 1.1 Write a keepalive comment frame on idle streams and end the stream on a failed write
- [ ] 1.2 Cap open streams per session, API token or scoped key; refuse with `429`
- [ ] 1.3 Cache the stream scope; re-derive before filtering events and every 5 seconds
- [ ] 1.4 Expose `beamfall_events_streams_open` on `/metrics`
- [ ] 1.5 Tests: keepalive, cap and release, reads per idle poll, periodic re-check, gauge

## 2. Verify and close

- [ ] 2.1 Run `go test -race ./internal/server/ -run 'Event'`
- [ ] 2.2 Run `make gate`
- [ ] 2.3 Run `openspec archive sse-stream-limits` after landing, before completing CORE-0729
