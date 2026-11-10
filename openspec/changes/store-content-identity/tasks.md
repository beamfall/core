## 1. Content identity

- [x] 1.1 Hash managed imports while copying into staging and store the content fingerprint
- [x] 1.2 Hash the staged convert artifact before admission and store its content fingerprint
- [x] 1.3 Tests for same bytes at two paths, new bytes at a reused path and convert output
- [x] 1.4 Refuse a managed import or convert whose fingerprint is live at another path or library

## 2. JSON validation

- [x] 2.1 Reject invalid JSON for job payloads, checkpoints and job events
- [x] 2.2 Reject invalid JSON for outbox payloads
- [x] 2.3 Tests for each entry point

## 3. Not-found semantics

- [ ] 3.1 Return not-found only for missing saved filter and smart collection rows
- [ ] 3.2 Propagate restricted-library lookup failures
- [ ] 3.3 Injected database failure tests

## 4. Verify and close

- [ ] 4.1 Run `make gate`
- [ ] 4.2 Run `openspec archive store-content-identity` after landing, before completing CORE-0698
