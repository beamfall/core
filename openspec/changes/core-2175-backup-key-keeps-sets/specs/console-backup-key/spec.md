## MODIFIED Requirements

### Requirement: The owner can see the backup key until it is confirmed

The server SHALL return the pending backup key only through the `revealBackupKey` mutation, only
to an Admin Session, and never to an API token of any tier. The key returned SHALL be the key whose
public wrap key every Backup Set taken since the escrow was created is sealed to, and the same key
confirming keeps. The `escrowID` returned SHALL confirm only that key.

#### Scenario: Owner reveals the pending key

- **WHEN** an Admin Session calls `revealBackupKey` while a bootstrap escrow is pending
- **THEN** the reply carries the confirmation ID, the backup key and the save-by date

#### Scenario: Anyone else is refused

- **WHEN** a session without an Admin Session, or any API token, calls `revealBackupKey`
- **THEN** the request is refused and no key is returned

### Requirement: Confirming the key removes the escrow

`acknowledgeBackupKey(escrowID)` SHALL confirm the revealed key without replacing it: it SHALL make
sure the installed public wrap key is that key's, writing it atomically if it is not, and only then
remove the escrow file. Every Backup Set taken before confirming SHALL open with the confirmed key.
After it, `revealBackupKey` SHALL be refused and `backupKeyStatus` SHALL report nothing pending. An
ID that does not confirm the active escrow's key SHALL be refused without changing state.

#### Scenario: Backups after confirming open with the revealed key

- **WHEN** the owner reveals the key, confirms it, and then takes a backup
- **THEN** the escrow file is gone and that backup opens with the revealed key

#### Scenario: Backups before confirming open with the revealed key

- **WHEN** a backup is taken, and the owner then reveals the key and confirms it
- **THEN** the escrow file is gone and that backup opens with the revealed key

#### Scenario: Stale confirmation

- **WHEN** `acknowledgeBackupKey` names an ID that does not confirm the active escrow's key
- **THEN** it is refused and the active escrow stays

### Requirement: Console Backups reminds the owner until the key is confirmed

While a backup key is pending, Console Backups SHALL show a warning that the key is not saved yet,
a step that reveals the key with copy and print, and a confirmation that calls
`acknowledgeBackupKey`. It SHALL NOT say that the server will replace the key or that backups taken
before confirming need another key. The console SHALL NOT store the key, log it or put it in a URL.

#### Scenario: Pending key warning

- **WHEN** the owner opens Console Backups with a key pending
- **THEN** the page warns that the key is not saved and does not say the key will be replaced

## ADDED Requirements

### Requirement: An unconfirmed backup key is kept until it is confirmed

The bootstrap escrow SHALL stay pending until the owner confirms it. Passing the save-by date
SHALL NOT wipe the escrow, change the installed public wrap key or mint a new key.

#### Scenario: Window passes without confirmation

- **WHEN** a backup is taken, the save-by date passes, another backup is taken and the owner then
  confirms the revealed key
- **THEN** both backups open with the confirmed key

### Requirement: Backup key status never carries the key

`backupKeyStatus` SHALL report whether a key is pending, the save-by date the window started with
and the whole days left until it, and SHALL NOT carry the key or the escrow ID.

#### Scenario: Status while a key is pending

- **WHEN** a bootstrap escrow is pending and the Console reads `backupKeyStatus`
- **THEN** the reply says a key is pending with its save-by date and days left, and holds neither
  the key nor the escrow ID
