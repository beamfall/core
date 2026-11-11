## 1. Runner

- [x] 1.1 Add `script/release-nightlies-local.sh` running the six tiers and recording each with provenance
- [x] 1.2 Record a failed or time-boxed tier red; leave the web-compat-nightly green to its owning script
- [x] 1.3 Refuse a dirty worktree and stop when HEAD moves; finish with `script/nightly-freshness.sh check`

## 2. Tests

- [x] 2.1 Add `script/release-nightlies-local_test.sh` with fake tiers only
- [x] 2.2 Run it from `script/nightly-freshness_test.sh`
- [x] 2.3 Keep the existing web-compat-nightly green guard test passing

## 3. Docs

- [x] 3.1 Add `docs/store/release-checklist.md` with the ceremony order

## 4. Verify and close

- [ ] 4.1 Run `make gate`
- [ ] 4.2 Run `openspec archive release-nightlies-local` after landing, before completing CORE-0539
