## 1. Server

- [x] 1.1 Store: rotate a device's session in one transaction; read a device's latest session
- [x] 1.2 `POST /api/web/session/renew` with its refusals and backoff
- [x] 1.3 Device ID and secret cookies last 400 days on enroll, setup, promote, renew and sign-in
- [x] 1.4 `POST /api/web/admin-signin` with per-source and server-wide backoff and one uniform 401
- [x] 1.5 Route registries (access class, management contract, wire-contract candidate); publishing a new wire-contract version is left to the coordinator
- [x] 1.6 Tests: renewal success and refusals, admin sign-in success and refusals, store method

## 2. Host command

- [x] 2.1 `beamfall pairing-code` with `--data-dir`, `--role`, `BEAMFALL_DATA_DIR` and the lock check
- [x] 2.2 Help text and README CLI table
- [x] 2.3 Tests modelled on `reset-admin-password`

## 3. Web

- [x] 3.1 Renew once and retry when the session check is refused
- [x] 3.2 Admin-password form on the sign-in page
- [x] 3.3 Tests for both

## 4. Docs, verify and close

- [x] 4.1 Lockout-and-recovery runbook names all three recovery paths
- [x] 4.2 `openspec validate --all --strict`
- [ ] 4.3 `make gate`
- [ ] 4.4 `openspec archive login-recovery` after landing, before completing CORE-1106
