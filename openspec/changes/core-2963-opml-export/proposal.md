# Export Subscriptions as OPML

Ticket CORE-2963

## Why

Managers need a portable copy of their feed Subscriptions for import into another server or reader.

## What Changes

- Add a Manager-session HTTP download at `GET /api/subscriptions/export.opml` covering all Libraries visible to the session.
- Export OPML 2.0 with feed URLs and titles. Omit authenticated Subscriptions by default. The `includePrivate=true` option includes their exportable feed URLs and titles. Always omit URLs with userinfo, fragments, or recognized credential query parameters.
- Add an Export OPML link to the Manager Subscriptions page for the default download.
- Register the additive HTTP route in the management contract. Per-Profile Follow export is deferred to work after CORE-2957.
