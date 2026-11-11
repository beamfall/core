## 1. Access log and request metrics

- [x] 1.1 Record the response status in the request tracker and wrap the whole of `ServeHTTP`
- [x] 1.2 Emit the `http request` line and set the request ID before any early refusal
- [x] 1.3 Report `beamfall_http_requests_total{status_class}` and the duration sum and count

## 2. Auth refusal audit records

- [ ] 2.1 Note refusal reasons in `internal/auth` from a closed vocabulary
- [ ] 2.2 Emit one `auth refused` record per refused request and count `beamfall_auth_refusals_total{reason}`
- [ ] 2.3 Note refused pairing enrollments in the server

## 3. Health

- [ ] 3.1 Derive `status` from `ready` and report `ffmpeg` / `ffprobe` presence

## 4. Runbooks

- [ ] 4.1 Document the real `--public-metrics` semantics and the new counters in the metrics runbook
- [ ] 4.2 Add the lockout and recovery runbook

## 5. Verify and close

- [ ] 5.1 Run `make gate`
- [ ] 5.2 Run `openspec archive operator-diagnosability` after landing, before completing CORE-0733
