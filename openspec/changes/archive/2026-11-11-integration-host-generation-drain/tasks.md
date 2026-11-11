## 1. Integration Host

- [x] 1.1 Close the lease drain signal only after retirement with no guarded call in flight
- [x] 1.2 Tests in `internal/integrationhost/generation_test.go`

## 2. Verify and close

- [ ] 2.1 Run `make gate`
- [ ] 2.2 Run `openspec archive integration-host-generation-drain` after landing, before completing CORE-0746
