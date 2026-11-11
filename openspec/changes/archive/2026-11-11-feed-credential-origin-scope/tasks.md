## 1. Credential scoping

- [x] 1.1 Export `scrape.SameOrigin` on the existing redirect-origin normalization
- [x] 1.2 Send the feed credential with an enclosure download only when it is same-origin with the feed URL

## 2. Tests

- [x] 2.1 `TestSameOrigin` unit cases for scheme, host case, ports, other hosts and bad URLs
- [x] 2.2 `TestAuditFeedAuth`: same-origin, other-port and redirecting enclosures against two local servers

## 3. Verify and close

- [ ] 3.1 Run `make gate`
- [ ] 3.2 Run `openspec archive feed-credential-origin-scope` after landing, before completing CORE-0745
