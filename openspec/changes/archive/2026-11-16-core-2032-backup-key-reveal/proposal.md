# Show the owner the backup key and confirm it is saved

Ticket: CORE-2032 (`queue:core:main`).

## Why

The server mints a backup key on first start and keeps it in a 30-day bootstrap escrow
(`<data-dir>/secrets/backup-key-bootstrap-escrow.json`), but nothing outside `internal/backup`
read the escrow or called its acknowledge path, and the console never showed the key. Restoring
an encrypted backup (console or `beamfall restore --key`) needs that key. When the 30 days ran
out the server wiped the escrow and minted a new key, so every backup taken until then could no
longer be opened with any key the owner could reach. An app-level test confirmed it: the owner
had no field that returned the key, and after the window lapsed a set taken before it was
refused with the freshly minted key.

## What Changes

- New owner GraphQL fields, all Admin-Session gated. Reveal and acknowledge are refused to every
  API token tier; the status, which carries no secret, follows the token tiers like other reads:
  - `backupKeyStatus` (query): whether a backup key is waiting to be saved, when its bootstrap
    window ends and the whole days left. It never carries the key.
  - `revealBackupKey` (mutation): the key the server will keep using once it is confirmed, with
    the escrow ID that confirms it. It is the key the existing acknowledge path derives, so the
    key derivation, the envelope format and the on-disk files do not change. It can be revealed
    again until it is confirmed and never after.
  - `acknowledgeBackupKey(escrowID)` (mutation): runs the existing
    `AcknowledgeBootstrapEscrow`, which installs the confirmed key's public wrap key and removes
    the escrow file. A stale escrow ID is refused.
- Console Backups shows a warning with the days left while the key is unconfirmed, a reveal step
  that shows the key with copy and print, says that backups taken before confirming use a
  temporary key, and a confirmation that the key is saved.
- The key is never logged, never put in a URL and never stored by the console.

## Not in this change

- A CLI path for headless owners. The CLI reaches a running server with admin API tokens, and a
  token must not be able to read the backup key. Headless owners use the console from another
  device.
- Re-sealing backups taken before confirmation with the confirmed key.
- The user-docs backup page lives outside this repository; dropping its "Not available yet"
  warning is left to the docs change that follows this one.

## Impact

- `internal/backup` (exported reveal of the pending key), `internal/graph` (three fields),
  `internal/app` (wiring), web console Backups page and strings, wire goldens.
