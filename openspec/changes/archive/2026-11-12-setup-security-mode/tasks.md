## 1. Server

- [x] 1.1 Store: read and write the mode in `server_config`; `BootstrapOwner` stores it
- [x] 1.2 Setup request takes `securityMode`
- [x] 1.3 Admin sign-in refuses in strict mode with identical accounting
- [x] 1.4 Password-checked mode change with backoff and audit
- [x] 1.5 `GET /api/setup` reports the mode; `POST /api/web/signin-security` and its registrations
- [x] 1.6 Tests: setup in each mode, strict and home sign-in, mode change, wrong or missing password, audit

## 2. Web

- [x] 2.1 Setup wizard security step
- [x] 2.2 Sign-in page hides the password form in strict mode
- [x] 2.3 Console setting
- [x] 2.4 Tests

## 3. Docs, verify and close

- [x] 3.1 Runbook paragraph: strict-mode recovery is `beamfall pairing-code`
- [x] 3.2 `openspec validate --all --strict`
- [ ] 3.3 `make gate`
- [ ] 3.4 `openspec archive setup-security-mode` after landing, before completing CORE-1110
