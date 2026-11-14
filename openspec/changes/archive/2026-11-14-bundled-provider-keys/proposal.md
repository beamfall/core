# Ship Beamfall's own provider keys in official builds

Ticket: CORE-1129 (`queue:core:main`).

## Why

A fresh server identifies nothing until the owner registers their own metadata API keys, which
most owners never do. Official Beamfall builds should carry Beamfall's own project keys so
metadata works out of the box, while an owner who saves a key of their own still wins. Some
sources also pair a project key with a per-user secret (TheTVDB's free key needs each user's
subscriber PIN), and a scraper login could carry only one secret.

## What Changes

- An official build may inject named default credentials at link time
  (`-X .../internal/app.BundledCredentials=ref=value,...`), read from
  `BEAMFALL_BUNDLED_CREDENTIALS` by `script/release.sh`. Source builds carry none.
- A bundled credential answers only when the owner has saved no secret under that AuthRef; saving
  one overrides it and clearing it restores the bundled default. The Console reports a bundled
  credential as configured.
- A login-bearer exchange sends every secret its `LoginSpec.SecretFields` names
  (plugin SDK `SecretFields`) beside its `SecretField`; a missing one fails the exchange.
- A cached login token is dropped when any credential it used is rotated or cleared.

## Impact

- Code: `internal/app` (credential source wiring), `internal/scrape` (login exchange and token
  cache), `script/release.sh`.
- Plugin SDK: `LoginSpec.SecretFields`; Film & TV gates TheTVDB on the subscriber PIN.
- A key in a binary is extractable; bundled keys are rate-limited project keys, not secrets that
  guard user data, and `go version -m` shows the injected flags.
