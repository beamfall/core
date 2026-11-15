# console-staged-restore Specification

## Purpose
Defines how an admin restores a Backup Set from the console without stopping the server: inspect the set, stage it, and have the restore apply on the next start with a rollback copy of the current data.

## Requirements

### Requirement: An admin inspects a backup set before restoring it

The console SHALL let an Admin Session open a published encrypted Backup Set with its backup key
and SHALL show the set's capture time, restore format, databases with their schema versions
against the versions this server supports, and whether this server can restore it, with the reason
when it cannot. The backup key SHALL never be stored, logged or returned.

#### Scenario: Compatible set

- **WHEN** the admin opens a set this server can restore with its backup key
- **THEN** the console lists its databases and versions and reports that it can be restored

#### Scenario: Newer set refused

- **WHEN** a database in the set has a schema version newer than this server supports
- **THEN** the console reports that the set cannot be restored and names the newer version

#### Scenario: Wrong key

- **WHEN** the backup key does not open the set
- **THEN** the console reports that the set could not be opened with this key

### Requirement: A staged restore applies on the next start with a rollback copy

The server SHALL stage a compatible set in its data directory and, on the next start before the
store opens, SHALL restore it through the offline restore path, keeping the previous data directory
as a rollback copy and recording the outcome. A failed restore SHALL put the original data back and
SHALL NOT retry; if it cannot, the server SHALL refuse to start.

#### Scenario: Applied on start

- **WHEN** a restore is staged and the server starts
- **THEN** the data directory holds the restored set, the previous data is kept as a rollback copy,
  and the staged copy is removed

#### Scenario: Restore fails

- **WHEN** the staged restore fails during start
- **THEN** the server starts on its original data, records the failure and does not retry it

#### Scenario: Identity differs

- **WHEN** the set's secrets or certificates differ from the server's and the admin did not choose
  to replace them
- **THEN** staging is refused with that reason

#### Scenario: Canceled

- **WHEN** the admin cancels a staged restore before a restart
- **THEN** nothing is applied on the next start

### Requirement: A staged restore holds the data directory until its outcome is safe

The restore SHALL run under the server lock, and a journal beside the data directory SHALL record
the rollback copy from before the data directory is renamed until the outcome is safe. While the
journal exists, no server SHALL start.

#### Scenario: Interrupted restore

- **WHEN** a start stopped after renaming the data directory, or a rollback could not put the
  original back
- **THEN** later starts refuse and name where the original data is, rather than starting on an
  empty data directory

#### Scenario: Second server during a restore

- **WHEN** another server starts while a staged restore is replacing the data directory
- **THEN** it refuses to start and does not open or create the data directory
