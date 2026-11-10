## 1. Implementation

- [x] 1.1 Render the app chrome and a skeleton in the Viewer area while the session is pending
- [x] 1.2 Add the identity-scoped in-memory viewer cache and clear it at every epoch transition
- [x] 1.3 Serve cached section items and revalidate in the background; abort abandoned fetches
- [x] 1.4 Entity page: cached or navigation-state title, parallel related-media request
- [x] 1.5 Web unit tests for skeleton, cache hit, identity isolation, clearing and abort

## 2. Close

- [ ] 2.1 Run `make gate`
- [ ] 2.2 Archive this change with `openspec archive web-viewer-instant-bootstrap`
