## 1. Store

- [ ] 1.1 Add `Store.BootstrapOwner`: owner Profile, owner designation, Manager Device and Session in one transaction, reusing a Profile left by a partial write when no Device exists
- [ ] 1.2 Add `Store.FirstRunSetupComplete`: an adult Profile and at least one Device exist

## 2. First-run wiring

- [ ] 2.1 `pairing.Service.Bootstrap` commits through `BootstrapOwner`
- [ ] 2.2 `bootstrapSetupToken` installs a token unless `FirstRunSetupComplete`

## 3. Tests

- [ ] 3.1 `TestAuditBootstrapRestartsAfterPartialOwnerWrite` (legacy partial write, restart, setup, restart)
- [ ] 3.2 Store fault-injection test: enrollment failure rolls back every first-run row; a reopened store completes setup

## 4. Verify and close

- [ ] 4.1 Run `make gate`
- [ ] 4.2 Run `openspec archive bootstrap-restart-recovery` after landing, before completing CORE-0757
