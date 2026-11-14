## 1. Prove

- [x] 1.1 Store test: one Profile-scoped query per 500 IDs and no other Profile's rows (`TestProgressByMediaBatchesPerProfile`)
- [x] 1.2 Store test: another Profile's progress never annotates the acting Profile's items (`TestAnnotateEngagementHidesAnotherProfilesProgress`)

## 2. Record

- [x] 2.1 Evidence doc mapping the ticket acceptance to `engagement`, with the ordinal-provenance finding
- [x] 2.2 File the ordinal-provenance follow-up ticket (CORE-1416)

## 3. Verify and close

- [ ] 3.1 Run `make gate`
- [ ] 3.2 Run `openspec archive source-item-playback-facts` after landing, before completing CORE-0353
