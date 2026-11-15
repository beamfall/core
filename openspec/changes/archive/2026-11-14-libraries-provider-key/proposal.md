# Missing provider keys on the Libraries page

Ticket: CORE-1534 (`queue:core:main`).

## Why

Matching uses metadata-provider keys that the active plugins declare. When a key is missing, the
items in a Library import unmatched, but the Libraries page gave no sign of it: the owner had to
know to open Plugins. The first-run wizard already lists the missing keys on its Plugins step.

## What Changes

- The console Libraries page lists every key that an active plugin requires and has not been
  configured, from `consoleConfig`'s scraper credential state, and lets the owner set it there
  through `setScraperCredential`.
- After a save, the row stays for that visit and shows the new status. Validate and Disable are
  offered like on the Plugins page, so the owner can clear the key again from the same row.
- The secret field is write-only on both surfaces: a stored value is never echoed back, and the
  field empties after a successful save.
- The provider-key row moves out of the Plugins section into a shared console component, so the
  two surfaces behave the same.

## Non-goals

- No server, schema or wire-contract change. The page uses the existing `consoleConfig`,
  `setScraperCredential`, `validateScraperCredential` and `disableScraperCredential` operations.
