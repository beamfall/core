## 1. Route credentials

- [x] 1.1 Add the credentials dimension to the `/v1` route contract and the device-Session check
- [x] 1.2 Open the three catalog reads to enrolled device Sessions

## 2. Client operations

- [x] 2.1 Add `POST /v1/media/{mediaID}/playback-info` with a bounded projection
- [x] 2.2 Add `PUT /v1/media/{mediaID}/progress` through a recorder shared with GraphQL
- [x] 2.3 Wire the recorder at the composition root

## 3. Contract and docs

- [x] 3.1 Update `docs/api/openapi.json` and regenerate the API reference
- [x] 3.2 Check credentials in `TestOpenAPISpecMatchesRoutes`
- [x] 3.3 Regenerate the wire-contract goldens and publish the new wire-contract version

## 4. Verify and close

- [x] 4.1 Run the focused tests, lint and `openspec validate --all --strict`
- [ ] 4.2 Run the wsl preflight and review the head
- [ ] 4.3 Archive this change with `openspec archive core-1694-public-client-api`
