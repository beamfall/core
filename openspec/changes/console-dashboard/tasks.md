## 1. Live session detail

- [ ] 1.1 Record the last playback decision per session and item in the playback service
- [ ] 1.2 Report `playMethod`, `playReason` and `bitrate` on `liveSessions`

## 2. Host metrics

- [ ] 2.1 Sample CPU, memory and network into a one-hour ring
- [ ] 2.2 Expose the ring as the admin field `hostMetrics`

## 3. Play history and rankings

- [ ] 3.1 Derive plays from playback events with profile, library and date filters
- [ ] 3.2 Expose `playHistory` and `playStats`

## 4. Console

- [ ] 4.1 Live sessions: method, reason, bitrate, progress, timed refresh
- [ ] 4.2 CPU, memory and network graphs
- [ ] 4.3 History page with filters, top profiles and most played

## 5. Verification

- [ ] 5.1 Verify in the running console on the demo seed with two concurrent sessions
