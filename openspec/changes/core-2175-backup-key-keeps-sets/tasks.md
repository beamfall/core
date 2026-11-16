## 1. Server

- [ ] 1.1 Acknowledging confirms the escrowed key: no derived replacement, the installed public wrap
      key is verified (and reinstalled atomically if needed) before the escrow is wiped
- [ ] 1.2 Reveal returns the escrowed key with a confirmation ID bound to its public fingerprint;
      acknowledging with any other ID is refused as changed
- [ ] 1.3 An unconfirmed escrow is kept past the end of its window; no wipe, no new key
- [ ] 1.4 Tests: seal, acknowledge, restore with the confirmed key; seal, pass the window, seal
      again, acknowledge, restore both; crash after the public key check retries cleanly; a
      bare escrow ID is refused; an old-binary acknowledgement journal still recovers

## 2. Console

- [ ] 2.1 Drop the "taken before you confirm" note, the replacement countdown and the "take a new
      backup now" advice in every locale
- [ ] 2.2 Vitest updated for the new copy

## 3. Docs

- [ ] 3.1 Backup Key glossary entry: the save-gate lasts until the owner confirms
