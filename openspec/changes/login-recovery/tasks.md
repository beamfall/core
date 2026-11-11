## 1. Server

- [ ] 1.1 Store: rotate a device's session in one transaction; read a device's latest session
- [ ] 1.2 `POST /api/web/session/renew` with its refusals and backoff
- [ ] 1.3 Device ID and secret cookies last 400 days on enroll, setup, promote, renew and sign-in
- [ ] 1.4 `POST /api/web/admin-signin` with per-source and server-wide backoff and one uniform 401
- [ ] 1.5 Route registries (access class, management contract, published manifests)
- [ ] 1.6 Tests: renewal success and refusals, admin sign-in success and refusals, store method

## 2. Host command

- [ ] 2.1 `beamfall pairing-code` with `--data-dir`, `--role`, `BEAMFALL_DATA_DIR` and the lock check
- [ ] 2.2 Help text and README CLI table
- [ ] 2.3 Tests modelled on `reset-admin-password`

## 3. Web

- [ ] 3.1 Renew once and retry when the session check is refused
- [ ] 3.2 Admin-password form on the sign-in page
- [ ] 3.3 Tests for both

## 4. Docs, verify and close

- [ ] 4.1 Lockout-and-recovery runbook names all three recovery paths
- [ ] 4.2 `openspec validate --all --strict`
- [ ] 4.3 `make gate`
- [ ] 4.4 `openspec archive login-recovery` after landing, before completing CORE-1106
