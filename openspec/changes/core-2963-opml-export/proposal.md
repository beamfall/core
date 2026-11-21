# Export Subscriptions as OPML

Ticket CORE-2963

## Why

Managers need a portable copy of their feed Subscriptions for import into another server or reader.

## What Changes

- Add a Manager-session HTTP download at `GET /api/subscriptions/export.opml` covering visible, unsealed Libraries.
- Export OPML 2.0 with feed URLs and titles. Omit authenticated Subscriptions and URLs with userinfo, a query string, or a fragment. Limit the document to 10,000 outlines and 64 MiB.
- Add an Export OPML action to the Manager Subscriptions page with localized failure messages.
- Register the additive HTTP route in the management contract. Per-Profile Follow export is deferred to work after CORE-2957.
