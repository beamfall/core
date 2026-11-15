# Server text localization

Status: accepted
Date: 2026-11-15

Realizes: CORE-2102 (OpenSpec change `core-2102-server-text-l10n`).

Beamfall 1.0 ships in English (`en`), French (`fr`), German (`de`), Spanish (`es`) and Portuguese
(`pt`). The clients localize their own interface. This page lists every kind of user-visible text
the Go server composes and says who localizes it.

## Two classes

- **(a) Stable code.** The server sends a stable identifier, and the client shows its own
  translated copy. English text may ride along as a diagnostic, but clients branch on the code,
  never on the words. This is the preferred class.
- **(b) Server-localized text.** The server renders the text from `internal/l10n` catalogs in the
  request locale. Use this only when the client has no copy of its own: text built from server
  state, or text that some client shows verbatim.

## Locale negotiation

`internal/l10n` vendors the shared locale declaration (`internal/l10n/locales.json`, a
byte-for-byte copy of the shared locale contract, CORE-2104) and keeps one flat catalog per
locale in `internal/l10n/catalog/<tag>.json`, with `{name}` placeholders. `l10n.Negotiate` picks
the locale in this order:

1. The profile's interface-locale preference, when it names a shipped locale. No profile field
   exists yet, so the server passes an empty preference (see Follow-ups).
2. The first `Accept-Language` entry with q > 0 that matches a shipped locale exactly, or by
   language when the shipped locale has no region (`pt-BR` resolves to `pt`, `de-CH` to `de`).
   Headers over 512 bytes are ignored.
3. The source locale, `en`.

`Server.ServeHTTP` negotiates once per request (`internal/server/server.go`) and stores the result
with `l10n.WithLocale`. Handlers read it back with `l10n.FromContext`. A missing key in a
non-source catalog falls back to the source text for that key.

## Inventory

| Text | Where | Class | What carries it |
|---|---|---|---|
| GraphQL errors (`clientError`, `store.ValidationError`) | `internal/graph`, `internal/store` | (a) | `extensions.code` from `graph.ClientErrorCode` plus stable English `message`. `TestGraphQLErrorSitesCarryCodes` requires the code. |
| Library folder check refusals (folder missing, can't open, not a folder, unreadable) | `internal/store/library_roots_check.go`, rendered in `internal/graph/graph_l10n.go` | (b) | `message` in the request locale. `extensions.code` stays `INVALID`. The admin console shows these verbatim. |
| "library root is required" | `internal/store/library_roots_check.go` | (a) | Stable message, code `INVALID`. Clients validate it before sending. |
| Watch Party home-network refusal | `internal/graph/graph_watchparty.go` | (a) | Code `FORBIDDEN`. The web shows its own copy. |
| Admin password minimum length | `internal/store/admin.go` | (a) | Stable message. The web checks the length first and shows its own copy. A dedicated REST code is a follow-up. |
| REST diagnostics (`writeError` `{"error": ...}`) | `internal/server` | (a) | HTTP status plus a lowercase diagnostic. Clients map the status and never show the text as prose. |
| Setup LAN hints on `lan_hostname_not_allowed` | `internal/server/web_handlers.go` | (b) text, (a) code | The `code` is stable and clients branch on it. The `error` text is rendered in the request locale for clients that show it. |
| Inbox notification titles (access request, extension request, Watch Party invite, Watch Party seek request) | `internal/graph/graph_l10n.go` | (b) | Rendered in the reader's request locale from the notification kind. The stored title stays in English for exports and webhooks. Other kinds keep their stored title. |
| Watch Party participant fallback name | `internal/graph/graph_l10n.go`, `graph_watchparty.go` | (b) | Rendered per request in the participant list and seek-request body. Roster and ended broadcasts go to many readers and use the source text (see Follow-ups). |
| Job progress messages | `store.JobProgress` producers (scan, ingest, artwork, migrate) | (a) | Stable English messages such as "Scanning", with counts and a unit. The web maps known messages to its own copy in `internal/web/app/src/lib/jobDisplay.ts`. |
| Activity log entries | `internal/store` | (a) | Stable action codes. Clients render them. |
| Push notifications | `internal/push` | (a) | Type and identifier only. No text leaves the server. |
| Path picker refusals | `internal/server` | (a) | Stable diagnostics that the web matches to its own copy. |
| Manifest default navigation and shelf titles | `internal/manifest/manifest.go` | (b), deferred | `LocaleString` maps with `en` and `fr` only. Clients pick the entry. Adding `de`, `es` and `pt` changes the client-pinned manifest golden (see Follow-ups). |
| Webhook and email notification text | `internal/webhook/format.go`, `internal/webhook/email.go` | (b), deferred | English only. A delivery has no request locale; it needs a per-endpoint locale setting (see Follow-ups). |

Plugin-contributed labels (display names, shelf titles) are plugin data delivered as `LocaleString`
maps and are localized by each plugin. They are out of scope here.

## Guard

`TestServerTextIsCodedOrCataloged` in `internal/conformance/server_text_l10n_test.go` scans every
non-test Go file under `cmd/` and `internal/`, except `internal/l10n`, `internal/apptest` and
`internal/web`. It fails when a change adds any of the following:

- a `store.Notification` with a string-literal `Title`;
- a `plugin.LocaleString` literal, or any map literal keyed by two or more shipped locale tags;
- a sentence written for a person (a capitalised word followed by at least two more words) passed
  to `writeError`, `writeCodedError`, `clientError`, `ValidationError` or `http.Error`, unless it
  is the source text of a catalog entry;
- a `writeCodedError` with a literal message, or without a stable snake_case code.

The guard does not yet cover job progress messages or webhook text, which are built outside these
writers.

The existing cases are listed in `knownLocaleMaps` and `knownSentenceMessages` in that test, and
the test fails when a listed case disappears. Do not add to those lists. Give new text a stable code
the client localizes, or add a key to `internal/l10n/keys.go` and every catalog.
`TestServerTextGuardCatchesViolations` proves that each rule fires.

The `internal/l10n` tests enforce catalog completeness:

- every `Key` in `keys.go` has a source entry, and every entry has a `Key`;
- every shipped locale translates every key, with the same placeholders;
- no translation copies the English unless it is listed as identical in that language;
- the vendored declaration matches the contract file byte for byte, once that file is in the tree.

## Adding server text

1. Prefer a stable code that the client localizes.
2. If the server has to render the text, add a `Key` to `internal/l10n/keys.go` and an entry to
   every file in `internal/l10n/catalog/`. Render it with
   `l10n.Text(l10n.FromContext(ctx), key, args)`, or wrap a refusal in `l10n.Error` so that logs and
   code mapping keep using the English text.
3. Add the row to the inventory above.

## Follow-ups

These need wire or contract changes that would make clients repin, so this change leaves them out:

- A profile interface-locale preference (store migration plus a GraphQL field), passed as the
  first argument to `l10n.Negotiate`.
- A message key in GraphQL error `extensions`, so clients can localize class (a) messages without
  matching English.
- A REST code for the admin password minimum length.
- `de`, `es` and `pt` entries for the manifest default titles, which means regenerating the manifest
  golden and having clients re-vendor it.
- A per-endpoint locale for webhook and email delivery.
- An empty participant name in Watch Party roster and ended broadcasts, so that each client renders
  its own fallback.
- The web sending its chosen interface locale in `Accept-Language`.
