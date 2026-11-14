## 1. Credentials

- [x] 1.1 Add `BundledCredentials` and a credential source that falls back to it only when no owner secret exists
- [x] 1.2 Wire it into the scrape engine and the Console credential status
- [x] 1.3 Inject `BEAMFALL_BUNDLED_CREDENTIALS` in `script/release.sh`

## 2. Scrape engine

- [x] 2.1 Send `LoginSpec.SecretFields` in the login exchange (landed on main in 55261ad3)
- [x] 2.2 Drop a cached token when any credential it used is invalidated
- [x] 2.3 Unit tests for fallback, override, multi-secret login and invalidation
