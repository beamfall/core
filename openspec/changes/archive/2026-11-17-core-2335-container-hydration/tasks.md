## 1. Scrape

- [x] 1.1 `Variables.Sequence`/`Index` and the `{sequence}`/`{index}` substitutions
- [x] 1.2 Item lookup after the main lookup, overriding fields and artwork, non-fatal on failure
- [x] 1.3 Container entity maps produce container results; maps naming an unknown ordinal are skipped

## 2. Identify

- [x] 2.1 Apply and Lookup read the ordinal from the plugin parser (auto-identify applies through Apply)
- [x] 2.2 Container results merge into the existing container Entity and apply its poster
- [x] 2.3 Tests: per-item lookup and container hydration
