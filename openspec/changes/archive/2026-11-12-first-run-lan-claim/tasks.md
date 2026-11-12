## 1. Server

- [ ] 1.1 `pairing.SetupRequest` gains a server-set `LANClaim` flag; `Bootstrap` accepts an empty token only when it is set and setup is pending
- [ ] 1.2 `POST /api/setup` sets the flag from the server's LAN test; `GET /api/setup` reports `tokenRequired`
- [ ] 1.3 The startup output names reachable local addresses for a wildcard bind
- [ ] 1.4 Tests: LAN without token succeeds; off-LAN without token, forwarded through an undeclared proxy, wrong token on LAN, and any request after setup are refused

## 2. Web

- [ ] 2.1 The setup wizard skips the token step when `tokenRequired` is false
- [ ] 2.2 Wizard tests for both paths

## 3. Verify and close

- [ ] 3.1 `openspec validate --all --strict`
- [ ] 3.2 `make gate`
- [ ] 3.3 `openspec archive first-run-lan-claim` after landing, before completing CORE-1105
