## 1. Server

- [ ] 1.1 `auth.ProfileHeader` and a mismatch check against the resolved scope
- [ ] 1.2 Refuse a mismatch in the per-route compatibility wrapper: GraphQL-shaped on `/api/graphql`, JSON error elsewhere
- [ ] 1.3 Go tests: mismatch refused before the handler on GraphQL and a REST route, match served, no header served, blank header ignored

## 2. Web

- [ ] 2.1 Send `X-Beamfall-Profile` from `lib/api.ts` (and the raw profile picture and artwork upload fetches) once the session spine knows the Profile
- [ ] 2.2 A `PROFILE_MISMATCH` refusal for the current Profile starts a new identity epoch; a stale one is ignored
- [ ] 2.3 Unit tests for the header and the reload; an e2e test with two pages on one session

## 3. Land

- [ ] 3.1 `openspec validate --all --strict`
- [ ] 3.2 Gate on the landing stack, then `openspec archive core-2923-request-profile-precondition`
