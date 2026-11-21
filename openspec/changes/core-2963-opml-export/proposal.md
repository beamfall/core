# Export Subscriptions as OPML

Ticket CORE-2963

## Why

Managers need a portable copy of their feed Subscriptions for import into another server or reader.

## What Changes

- Add a Manager-session HTTP download at `GET /api/subscriptions/export.opml` covering visible, unsealed Libraries.
- Read every Subscription from the store. By default export only feeds with positive evidence of being public: an http(s) URL with no auth reference, userinfo, query string or fragment. The store records no directory provenance yet, so a clean URL is the only such evidence.
- With `includePrivate=true`, also export unclassified and auth-reference feeds with their complete URL (userinfo removed), the URL host as the outline text and no stored title. The OPML importer never stores a URL, or the feed's host, as a Subscription title.
- Never export an auth reference, URL userinfo or this server's URL: skip feeds on the server's own host, comparing IP literals as addresses.
- Fail the download if the full document exceeds the importer's 10,000-outline or 64 MiB limit.
- Add an Export OPML action to the Manager Subscriptions page with localized failure messages.
- Register the additive HTTP route in the management contract. Per-Profile Follow export is deferred to work after CORE-2957.
