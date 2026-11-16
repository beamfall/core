## 1. Locale and catalogs

- [x] 1.1 Add `internal/l10n` with the vendored locale declaration, catalogs and negotiation
- [x] 1.2 Resolve the request locale in the HTTP entry point

## 2. Server text

- [x] 2.1 Localize notification inbox titles by kind and the participant fallback name
- [x] 2.2 Localize the library-folder check messages
- [x] 2.3 Localize the setup LAN hints carried with `lan_hostname_not_allowed`

## 3. Guard and inventory

- [x] 3.1 Add the conformance test for new user-facing server text
- [x] 3.2 Write the inventory in `docs/architecture/server-text-localization.md`
- [ ] 3.3 After the shared declaration lands, confirm `internal/l10n/locales.json` is byte-identical to `contracts/locales/locales.json`
- [ ] 3.4 Run `make gate`
