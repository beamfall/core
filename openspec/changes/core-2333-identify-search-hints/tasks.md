## 1. Server

- [x] 1.1 Repin plugin-sdk to v0.4.8
- [x] 1.2 Scan merges parser hints with embedded-tag hints named by the bundle, parser value first
- [x] 1.3 The scrape engine substitutes `{hint.<key>}` in a SearchTemplate's query, query-escaped, missing hints empty
- [x] 1.4 A scraper missing a required search hint is skipped; automatic identification falls through to the next scraper for the class
- [x] 1.5 Automatic and manual identification pass the item's hints to the search

## 2. Tests

- [x] 2.1 Scrape test: substitution with escaping, a missing optional hint renders empty, a missing required hint sends no request
