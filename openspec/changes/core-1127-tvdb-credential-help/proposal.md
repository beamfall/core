# Present TheTVDB's subscriber PIN in the Console and setup

Ticket: CORE-1127 (`queue:core:main`).

## Why

TheTVDB v4 `POST /login` takes `{apikey, pin}`. Confirmed against the official v4 swagger
(`docs/swagger.yml`, version 4.8.0, sha256
`ff4a162b2b9192f83abdda672fa64c4f88fe5c1413c6c9eccc7b46a752c633f3`) and the v4-api README: `pin`
is the end user's subscriber PIN, required for user-supported project keys, and the token in
`data.token` is valid for one month.

CORE-1129 (completed) already made the login carry both secrets: the PIN is the scraper's
`AuthRef` (`tvdb-subscriber-pin`) and the project key is a `Login.SecretFields` entry
(`{"apikey": "tvdb-api-key"}`). That decision supersedes this ticket's original optional-PIN shape,
so CORE-1127 narrows to presentation: the owner must see that the value is a subscriber PIN, not an
API key, and where to subscribe.

## What Changes

- A scraper may declare `CredentialHelp` (plugin-sdk v0.3.6): a localized label for its AuthRef
  credential, an https help link and the link's label. Core reads it through
  `plugin.ScraperExtensionsOf`, which returns nothing against the pinned plugin-sdk v0.3.5; the repin
  maps the SDK field there.
- `consoleConfig.plugins.scrapers.credentialHelp { label helpURL helpLabel }` carries it to clients.
  It is plugin data, never a secret value, and is omitted for a scraper without an AuthRef.
- The Console Plugins provider-key form and the setup wizard label the field with the plugin's
  label and show the help link (opened with `rel="noopener noreferrer"`, only for https URLs).
- The PIN is saved through the existing `setScraperCredential` path, stored in the secure store like
  every AuthRef secret and never returned on the wire.
- "Without a PIN": the AuthRef is required, so a missing PIN reports `CredentialMissingError` and
  sends no login request (CORE-1129 semantics), covered by a recorded-fixture test.

## Impact

- Wire contract: one new optional field. The development wire contract now differs from the newest
  published artifact (`wire-contract-v2.9.0.json`); the landing batch publishes the next artifact.
- No migration, no new Store method.
