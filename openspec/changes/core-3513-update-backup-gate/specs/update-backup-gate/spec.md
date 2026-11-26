# update-backup-gate Specification Delta

## ADDED Requirements

### Requirement: Signed local update staging and offline apply enforce the backup update gate
Core SHALL support staging signed local release distribution directories (`stageCoreUpdate` GraphQL mutation and `beamfall upgrade stage` CLI command) and applying staged updates while the server is stopped (`beamfall upgrade apply`), verifying the distribution Ed25519 signature and SHA-256 digests against `release.PinnedKey()` and enforcing the ADR-0192 backup update gate.

#### Scenario: Unsigned or tampered dist directory is rejected before staging
- **WHEN** an operator stages a distribution directory whose `manifest.json` lacks a valid signature from `release.PinnedKey()` or whose binary artifact digest mismatches `SHA256SUMS`
- **THEN** staging fails closed and no staged update marker is written

#### Scenario: Hard update gate refuses staging and apply without a fresh verified backup
- **WHEN** `UpgradeGate` is `hard` and `UpdateGateClear()` is `false`
- **THEN** staging via `stageCoreUpdate` or `beamfall upgrade stage`, and applying via `beamfall upgrade apply`, refuse with an error identifying the blocked backup gate

#### Scenario: Soft update gate blocks staging unless a matching elevated bypass is recorded
- **WHEN** `UpgradeGate` is `soft` and `UpdateGateClear()` is `false` and no matching `LastUpgradeBypass` exists for the dist version
- **THEN** staging refuses until either a fresh verified backup clears the gate or an Admin Session authorizes an elevated bypass for that target version

### Requirement: Elevated soft-gate bypass requires step-up Admin re-authentication and typed confirmation
An operator bypassing a blocked `soft` update gate (`authorizeBackupUpgradeBypass`) SHALL authenticate with an active `manager` Admin Session (denied to every API token tier), re-verify the Admin password, provide typed confirmation naming the target version and any failed Backup Set ID, and provide a non-empty audit reason.

#### Scenario: Hard policy, wrong password, missing confirmation, or API token refuses bypass
- **WHEN** a caller invokes `authorizeBackupUpgradeBypass` using an API token, an invalid Admin password, a confirmation omitting the target version or active failed Backup Set ID, an empty reason, or while `UpgradeGate` is `hard`
- **THEN** the mutation fails closed and no bypass record is stored

#### Scenario: Valid step-up bypass records durable audit, activity, log, and webhook
- **WHEN** a manager Admin Session invokes `authorizeBackupUpgradeBypass` under `soft` with valid Admin password, matching confirmation, and reason
- **THEN** `LastUpgradeBypass` is persisted on `backup-status.json`, a Console activity entry is written, a warning is logged, and a content-free `backup.upgrade_bypassed` outbox event is emitted

### Requirement: Boot-time upgrade detection runs before database migrations
On startup prior to `store.Open` running SQLite migrations, Core SHALL compare the booting release version against `LastBinaryVersion` recorded in `backup-status.json`.

#### Scenario: Booting a new release version under hard gate without verified pass refuses before migration
- **WHEN** `LastBinaryVersion` differs from the booting release version, `UpgradeGate` is `hard`, `UpdateGateClear()` is `false`, and `--allow-unverified-upgrade` is not set
- **THEN** startup refuses before `store.Open` opens or migrates the database and leaves `LastBinaryVersion` unchanged

#### Scenario: Booting a new release version under soft gate proceeds with unverified banner, log, and webhook
- **WHEN** `LastBinaryVersion` differs from the booting release version, `UpgradeGate` is `soft`, and `UpdateGateClear()` is `false`
- **THEN** startup proceeds, updates `LastBinaryVersion`, activates `UnverifiedUpgradeBanner` (dismissible via `acknowledgeBackupBanner`), logs a warning, and appends a `backup.upgrade_without_backup` (or `backup.upgrade_bypassed` when matched by a recorded bypass) outbox event

### Requirement: Console Operations update gate exposes bypass re-auth, dist staging, and banner ack
Console Operations (`Operations.tsx`) SHALL display the active gate policy (`Soft` vs `Hard block`), allow authorized managers to open "Review elevated bypass" (password re-auth, typed confirmation, audit reason) under `soft`, stage a verified local release directory, and acknowledge an active unverified-upgrade banner.

#### Scenario: Console renders soft bypass form and blocks bypass UI under hard policy
- **WHEN** `backupStatus.updateGateClear` is `false` and `updateAvailable` is `true`
- **THEN** `soft` enables step-up bypass and staging controls once cleared/bypassed, while `hard` disables bypass and directs the operator to create and verify a Backup Set first
