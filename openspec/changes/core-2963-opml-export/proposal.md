# Export Subscriptions as OPML

Ticket CORE-2963

## Why

Managers need a portable copy of their feed Subscriptions for import into another server or reader.

## What Changes

- Add a Manager-session HTTP download at `GET /api/subscriptions/export.opml` covering visible, unsealed Libraries.
- Export every Subscription as OPML 2.0 with its feed URL and title, read from the store. Private feeds (an auth reference, URL userinfo or a secret-looking query parameter) are left out unless the Manager passes `includePrivate=true`.
- Never export an auth reference, credential or this server's URL: strip userinfo, secret query parameters and fragments, skip feeds on the server's own host (IP literals compared as addresses) and feeds whose path holds an opaque secret.
- Fail the download if the full document exceeds the importer's 10,000-outline or 64 MiB limit.
- Add an Export OPML action to the Manager Subscriptions page with localized failure messages.
- Register the additive HTTP route in the management contract. Per-Profile Follow export is deferred to work after CORE-2957.
