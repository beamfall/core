## 1. Server

- [ ] 1.1 Re-derive a web UI decision when authorization is its only mismatch
- [ ] 1.2 Keep native credentials, refused subjects, expired decisions and stale submitted
      decisions refused
- [ ] 1.3 Regression tests in `internal/server/compat_test.go`

## 2. Verify and close

- [ ] 2.1 Run `make gate`
- [ ] 2.2 Run `openspec archive web-compat-auth-change` after landing, before completing CORE-0042
