## 1. Server

- [x] 1.1 `auth.ProfileHeader` and a mismatch check against the resolved scope
- [x] 1.2 Refuse a mismatch in the per-route compatibility wrapper: GraphQL-shaped on `/api/graphql`, JSON error elsewhere
- [x] 1.3 Enforce the header at every scope resolution (`auth.ErrProfileMismatch`); end a bound event stream on mismatch; run Reveal, relock and playback end only while the session is bound to the request's Profile
- [x] 1.4 Go tests: mismatch refused before the handler on GraphQL and a REST route, match served, no header served, blank header ignored

## 2. Web

- [x] 2.1 Send `X-Beamfall-Profile` from `lib/api.ts` (and the raw profile picture and artwork upload fetches) once the session spine knows the Profile
- [x] 2.2 Name the Profile on the event stream, keepalive writes and the mutations that open an epoch
- [x] 2.3 A `PROFILE_MISMATCH` refusal for the current Profile starts a new identity epoch; a stale one is ignored
- [x] 2.4 Unit tests for the header and the reload; an e2e test with two pages on one session

## 3. Land

- [x] 3.1 `openspec validate --all --strict`
- [x] 3.2 Gate on the landing stack, then `openspec archive core-2923-request-profile-precondition`
