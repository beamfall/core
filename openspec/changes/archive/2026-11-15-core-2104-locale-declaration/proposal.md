# Shared locale declaration

Ticket: CORE-2104 (`queue:core:main`).

## Why

Every client kept its own hand-written list of offered languages, so adding a language meant
editing each list and each l10n check by hand, and nothing caught a list that drifted or a shipped
language whose catalog was incomplete.

## What Changes

- Core publishes `contracts/locales/locales.json`: schema version, source locale and one entry per
  locale (BCP-47 tag, English and native name, direction, status `shipped` or `in-progress`).
- Consumers vendor a byte-identical copy and derive both their runtime locale list and their l10n
  check's required-locale set from it. The web app vendors it at
  `internal/web/app/src/l10n/locales.json`.
- An `in-progress` locale is built and key-parity checked but never offered; its missing keys fall
  back to the source locale per key.
- A test fails when a hard-coded locale list reappears or a shipped locale is missing or incomplete.

## Non-goals

- No new language is added by this change.
- Client repositories adopt the declaration in their own repositories.
