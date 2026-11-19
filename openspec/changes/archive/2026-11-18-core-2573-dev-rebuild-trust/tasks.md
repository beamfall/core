## 1. Provenance

- [x] 1.1 `BuildRecord.Dev`, set only by `ParseDevBuildManifest`, included in `BuildCatalog.Digest`
- [x] 1.2 `VerifyBuildProvenance` admits a well-formed differing digest for a dev record the enrollment is already mapped to
- [x] 1.3 `EnrollmentBindingFor` keeps the exact digest check

## 2. Tests

- [x] 2.1 Refused, then admitted after the operator writes the record, then admitted after two rebuilds, on one server instance
- [x] 2.2 A device without the mapping is neither mapped nor admitted with a rebuilt digest; a malformed digest is refused
- [x] 2.3 Removing the record revokes the mapped device; a release record stays exact for a mapped device

## 3. Verify and close

- [ ] 3.1 `GOWORK=off go test ./internal/capabilities/ ./internal/server/ -run 'Compat|DevBuild|FirstParty|Shipped|Rebind|Enrollment|Provenance'`
- [ ] 3.2 Landing gate on the stack head
- [ ] 3.3 `openspec archive core-2573-dev-rebuild-trust` after landing, before completing CORE-2573
