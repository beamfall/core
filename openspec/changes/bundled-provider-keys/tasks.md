## 1. Credentials

- [ ] 1.1 Add `BundledCredentials` and a credential source that falls back to it only when no owner secret exists
- [ ] 1.2 Wire it into the scrape engine and the Console credential status
- [ ] 1.3 Inject `BEAMFALL_BUNDLED_CREDENTIALS` in `script/release.sh`

## 2. Scrape engine

- [ ] 2.1 Send `LoginSpec.SecretFields` in the login exchange
- [ ] 2.2 Drop a cached token when any credential it used is invalidated
- [ ] 2.3 Unit tests for fallback, override, multi-secret login and invalidation
