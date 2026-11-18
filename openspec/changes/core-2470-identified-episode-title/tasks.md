## 1. Server

- [x] 1.1 Add `scan.IdentifiedMediaTitler` and the optional `identify.IdentifiedMediaTitleSource`
- [x] 1.2 `Apply` passes the lookup's plugin Media field values to the source and writes its non-blank title; otherwise the CORE-1713 behaviour holds
- [x] 1.3 `PreviewLookup` gives the Fix-Match preview the title `Apply` writes
- [x] 1.4 The app parser adapter implements the hook over the primary file, falling back to `MediaTitle`

## 2. Tests

- [x] 2.1 Identify test: field values reach the source, a blank or failed identified title falls back, a source without the hook keeps `ParsedMediaTitle`
- [x] 2.2 App test: a parser with the hook titles the identified Media from the lookup's field; one without keeps its `MediaTitle`
- [x] 2.3 Graph test: the Fix-Match preview proposes the identified title
- [ ] 2.4 Scoped checks on the lane head

## 3. Rollout

- [ ] 3.1 Repin Film & TV to the release that implements `IdentifiedMediaTitle`
- [ ] 3.2 Tower: refresh Ted Lasso; S03E01, S03E02 and S03E08-E10 show their episode names; archive
