## 1. Handshake rebind

- [x] 1.1 `rebindVerifiedBuild` in `internal/server/compat_enrollment.go`: native device credential only, `EnrollmentBindingFor` against the server-held enrollment (rollback floor is the downgrade bound), only the caller's row
- [x] 1.2 Call it in `acceptCompatHandshake` before each evaluation, so the handshake sees the new mapping

## 2. Tests

- [x] 2.1 Upgrade to a newer shipped build is servable without re-pairing
- [x] 2.2 A device with no mapping (pre-0112 or no Client Contract) obtains one
- [x] 2.3 Below the rollback floor, tampered digest, wrong pinned surface, wrong role, wrong distribution: no mapping, refused
- [x] 2.4 Sibling device rows are untouched by another device's rebind
- [x] 2.5 A newer verified build that needs a newer server is mapped and refused as server-too-old, rollback to the previous build is servable, and the newer build is servable once supported

## 3. Verify and close

- [ ] 3.1 `GOWORK=off go test ./internal/server/ -run 'Compat|FirstParty|Shipped|Rebind|Enrollment'` and `./internal/store/ -run DeviceBuild`
- [ ] 3.2 `make gate-preflight`, then `make gate` on the landing head
- [ ] 3.3 `openspec archive core-1393-native-build-rebind` after landing, before completing CORE-1393
