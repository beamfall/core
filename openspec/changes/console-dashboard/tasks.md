## 1. Live session detail

- [x] 1.1 Record the last playback decision per session and item in the playback service
- [x] 1.2 Report `playMethod`, `playReason` and `bitrate` on `liveSessions`

## 2. Host metrics

- [x] 2.1 Sample CPU, memory and network into a one-hour ring
- [x] 2.2 Expose the ring as the admin field `hostMetrics`

## 3. Play history and rankings

- [x] 3.1 Derive plays from playback events with profile, library and date filters
- [x] 3.2 Expose `playHistory` and `playStats`

## 4. Console

- [x] 4.1 Live sessions: method, reason, bitrate, progress, timed refresh
- [x] 4.2 CPU, memory and network graphs
- [x] 4.3 History page with filters, top profiles and most played

## 5. Verification

- [x] 5.1 Verify in the running console on the demo seed with two concurrent sessions
