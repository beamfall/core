# Localize server-originated user-facing text

Ticket: CORE-2102 (`queue:core:main`).

## Why

Beamfall 1.0 ships in English, French, German, Spanish and Portuguese, but the server still composes
some user-visible text in English only: notification inbox titles, the library-folder check messages
the admin console shows verbatim, the setup "open by IP address" hints, and the default shelf titles
in the client manifest (which carry only `en` and `fr`). Nothing stops a new English sentence from
reaching users without a code or a translation.

## What Changes

- Add `internal/l10n`: the vendored shared locale declaration (`locales.json`, CORE-2104 schema),
  per-locale message catalogs for every shipped locale, locale negotiation (profile preference,
  then `Accept-Language`, then `en`) and a request-context locale.
- Resolve the request locale once in the HTTP entry point and use it when the server renders
  class (b) text: notification titles, library-folder check messages and the setup LAN hints.
- Build the manifest's default shelf and section titles from the catalog for every shipped locale.
- Classify every server-composed user-visible string as (a) a stable code the client localizes or
  (b) server-localized text, in `docs/architecture/server-text-localization.md`.
- Add a conformance test that fails when a new user-facing server sentence, notification title or
  manifest title is added without a catalog entry, or when a catalog drifts from the shipped locales.

## Non-goals

- Wire or contract changes that would force client repins: a profile UI-locale field, a message
  key in GraphQL error extensions, or a webhook endpoint locale. These are follow-ups.
- Translating GraphQL `clientError` messages and REST diagnostics. Clients branch on their stable
  English text and their `extensions.code` / HTTP status today; they stay class (a).
- Client changes (web app, contracts/locales); another change owns them.

## Capabilities

### New Capabilities

- `server-text-localization`: how the server negotiates a locale and which server text is
  localized versus carried as a stable code.

### Modified Capabilities

None.
