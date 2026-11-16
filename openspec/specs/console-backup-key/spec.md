# console-backup-key Specification

## Purpose
Defines how the console shows the owner the pending backup key so they can record it before it is confirmed, and how the server limits that reveal to an Admin Session.

## Requirements

### Requirement: The owner can see the backup key until it is confirmed

The server SHALL return the pending backup key only through the `revealBackupKey` mutation, only
to an Admin Session, and never to an API token of any tier. The key returned SHALL be the one the
existing escrow acknowledge path installs for that escrow ID. `backupKeyStatus` SHALL report
whether a key is pending, when its bootstrap window ends and the whole days left, and SHALL NOT
carry the key or the escrow ID.

#### Scenario: Owner reveals the pending key

- **WHEN** an Admin Session calls `revealBackupKey` while a bootstrap escrow is active
- **THEN** the reply carries the escrow ID, the backup key and the window's end

#### Scenario: Anyone else is refused

- **WHEN** a session without an Admin Session, or any API token, calls `revealBackupKey`
- **THEN** the request is refused and no key is returned

### Requirement: Confirming the key removes the escrow

`acknowledgeBackupKey(escrowID)` SHALL run the existing escrow acknowledge path, which installs the
confirmed key's public wrap key and removes the escrow file. After it, `revealBackupKey` SHALL be
refused and `backupKeyStatus` SHALL report nothing pending. A stale escrow ID SHALL be refused
without changing state.

#### Scenario: Backups after confirming open with the revealed key

- **WHEN** the owner reveals the key, confirms it, and then takes a backup
- **THEN** the escrow file is gone and that backup opens with the revealed key

#### Scenario: Stale confirmation

- **WHEN** `acknowledgeBackupKey` names an escrow ID that is not the active one
- **THEN** it is refused and the active escrow stays

### Requirement: Console Backups reminds the owner until the key is confirmed

While a backup key is pending, Console Backups SHALL show a warning stating the days left, a step
that reveals the key with copy and print, a note that backups taken before confirming use a
temporary key, and a confirmation that calls `acknowledgeBackupKey`. The console SHALL NOT store
the key, log it or put it in a URL.

#### Scenario: Pending key warning

- **WHEN** the owner opens Console Backups with a key pending and 12 days left
- **THEN** the page warns that the key is not saved and that 12 days are left
