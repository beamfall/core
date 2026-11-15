- [x] Confirm `/login {apikey, pin}` against TheTVDB v4 swagger 4.8.0 and the README
- [x] `plugin.ScraperExtensions.CredentialHelp` read through `ScraperExtensionsOf`
- [x] `consoleConfig` scraper `credentialHelp`, omitted without an AuthRef; regenerated goldens
- [x] Console Plugins and setup wizard show the label and the https help link
- [x] Tests: login with the PIN (body only, never a header or query), missing PIN fails without a
      login request, graph field present and secret-free, web label and link
- [ ] After plugin-sdk v0.3.6 is tagged and pinned: map `ScraperSpec.CredentialHelp` in
      `ScraperExtensionsOf`
- [ ] Landing batch: publish the next wire-contract artifact
