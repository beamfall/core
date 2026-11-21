# Export Subscriptions as OPML

Ticket CORE-2963

## Why

Managers need a portable copy of their feed Subscriptions for import into another server or reader.

## What Changes

- Add a Manager-session HTTP download at `GET /api/subscriptions/export.opml` covering visible, unsealed Libraries.
- Export OPML 2.0 with explicitly approved feed URLs and public titles. Managers can opt in to authenticated feeds whose URLs are approved, with the URL as the outline text.
- Fail the download if the full document exceeds the importer's 10,000-outline or 64 MiB limit.
- Add an Export OPML action to the Manager Subscriptions page with localized failure messages.
- Register the additive HTTP route in the management contract. Per-Profile Follow export is deferred to work after CORE-2957.
