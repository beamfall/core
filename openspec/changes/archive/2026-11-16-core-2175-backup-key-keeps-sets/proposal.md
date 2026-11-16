# Confirming the backup key keeps every earlier backup set restorable

Ticket: CORE-2175 (`queue:core:main`).

## Why

Every Backup Set is sealed to the public wrap key installed in the data directory. While the
bootstrap escrow is pending that is the bootstrap key's public key. Confirming the key
(`AcknowledgeBootstrapEscrow`) derived a second key from the bootstrap key, installed its public
key and wiped the bootstrap key, so every set taken before the owner confirmed opened only with a
key nobody held any more. If the owner never confirmed, the 30-day window ended with the escrow
wiped and a new key minted, which orphaned the earlier sets the same way. CORE-2032 added a console
note asking the owner to take a new backup after confirming; that is a warning, not a fix.

The derived key added no secrecy: it is a public function of the escrow file's own contents (key
and ID), so anyone who could read the escrow could compute it.

## What Changes

- The key the owner confirms is the escrowed key itself: the key whose public wrap key every set
  has been sealed to since the escrow was created. Confirming checks that the installed public wrap
  key matches it (reinstalling it atomically if not) and only then wipes the escrow. No set is
  re-sealed, and the envelope format, the escrow file and the backup fixtures do not change.
- `revealBackupKey` returns that key. Its confirmation ID is the escrow ID bound to the key's public
  fingerprint, so a confirmation sent from a page that revealed a different key (for example a
  console tab opened before this change, which showed the derived key) is refused as changed and
  the console reveals the key again.
- An unconfirmed escrow is kept until the owner confirms it. Reaching the end of the window no
  longer wipes the escrow or mints a new key. `expiresAt` and `daysLeft` stay on the wire as the
  save-by date the window started with; nothing happens when it passes.
- An acknowledgement journal left by an older binary that crashed mid-acknowledgement is still
  recovered as before.
- Console Backups drops the "Backups taken before you confirm use a temporary key" note, the
  countdown that said the server would replace the key, and the "take a new backup now" advice
  after confirming, in every locale.

## Not in this change

- Sets orphaned before this change by an expired escrow (the bootstrap key is already gone) or by an
  earlier acknowledgement under the old derivation (the bootstrap key is already wiped).
- Retiring the `expiresAt` and `daysLeft` wire fields.

## Impact

- `internal/backup` (escrow acknowledge, reveal, expiry), `internal/app` (escrow adapter),
  `internal/graph` (comments only), web console Backups strings and component, the
  `console-backup-key` spec, the Backup Key glossary entry.
