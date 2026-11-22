- [x] Catalog migration 0129 for feed health state; schema freeze count bumped
- [x] Store health layer: failure, success, retire, move observation, refresh rate limit
- [x] Scrape `FetchFeed`: hand-followed redirects, permanent-move detection, credential kept on the
      original origin; new-feed-URL declaration parser
- [x] Poller: due check, interval clamp, jittered capped backoff, error classes, 410 retire, move
      rewrite after two observations; poll job every 15 minutes
- [x] Graph health fields, `refreshSubscription`, `refreshAllSubscriptions`; regenerated goldens
- [x] Tests: each error class, 30-day failing feed, 410, moved URL, refresh rate limit, Graph fields
- [ ] Landing batch: publish the next wire-contract artifact
