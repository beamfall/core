## 1. Scrape

- [ ] 1.1 `Variables.Sequence`/`Index` and the `{sequence}`/`{index}` substitutions
- [ ] 1.2 Item lookup after the main lookup, overriding fields and artwork, non-fatal on failure
- [ ] 1.3 Container entity maps produce container results; maps naming an unknown ordinal are skipped

## 2. Identify

- [ ] 2.1 Apply passes the parsed ordinal (auto-identify from the scan parse, manual apply from the parser)
- [ ] 2.2 Container results merge into the existing container Entity and apply its poster
- [ ] 2.3 Tests: per-item lookup and container hydration
