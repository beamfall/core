## 1. Server

- [x] 1.1 Store: insert-if-absent admin password that opens no window by itself
- [x] 1.2 Auth: first-password set with Manager, adult and owner checks, throttle and audit
- [x] 1.3 Graph: `verifyAdminSession` reads `createFirst`
- [x] 1.4 Tests: no password, set, post-set, non-manager, non-owner, throttled, audit

## 2. Web

- [x] 2.1 Console gate set-password form (choose and confirm) shown from the setup state
- [x] 2.2 Tests

## 3. Verify and close

- [x] 3.1 `openspec validate --all --strict`
- [ ] 3.2 `make gate`
- [ ] 3.3 `openspec archive first-admin-password` after landing, before completing CORE-1132
