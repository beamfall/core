## 1. Server

- [x] 1.1 App-level test confirming the owner could not obtain the key (CORE-2032 criterion 1)
- [ ] 1.2 `backup.RevealBootstrapEscrow` returns the key the acknowledge path will install
- [ ] 1.3 `backupKeyStatus`, `revealBackupKey`, `acknowledgeBackupKey`, Admin-Session only,
      reveal and acknowledge denied to every API token tier
- [ ] 1.4 Tests: reveal, acknowledge removes the escrow, stale ID refused, expiry, non-owner and
      token refusal, a backup taken after confirming opens with the revealed key
- [ ] 1.5 Wire goldens regenerated

## 2. Console

- [ ] 2.1 Backups warns with the days left while the key is unconfirmed
- [ ] 2.2 Reveal with copy and print, confirm calls `acknowledgeBackupKey`
- [ ] 2.3 Vitest for status, reveal, confirm and the refused confirm

## 3. Docs

- [ ] 3.1 User-docs backup page drops "Not available yet" (outside this repository)
