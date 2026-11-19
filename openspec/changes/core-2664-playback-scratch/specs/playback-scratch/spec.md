## ADDED Requirements

### Requirement: Exclusive live playback scratch ownership

A playback manager SHALL exclusively lock its scratch root before accessing session entries. It SHALL reserve each fresh directory name and establish ownership by exclusive creation. Cleanup SHALL wait for all directory users, keep failed removals discoverable for retry, and release the root only once shutdown has joined admitted scratch work and attempted outstanding cleanup.

#### Scenario: Scratch root still has an owner

- **WHEN** a second manager opens the same root or an alias while the first still owns it
- **THEN** construction fails before sweeping or allocating

#### Scenario: A detached directory is still in use

- **WHEN** a session is detached with an outstanding artifact reader or writer
- **THEN** encrypted readers lose access before cleanup waits for release, and directory removal waits for the final user

### Requirement: Confined scratch reconciliation

Live playback SHALL open regular artifact inputs within their owned directory without following symlinks or waiting for FIFO writers. A sweep SHALL preserve live paths and report unresolved ownership, pending cleanup and removal failures. An encrypted Library SHALL reconcile scratch outside the Store lock and refuse publication when reconciliation fails.

#### Scenario: Reconciliation cannot establish cleanup

- **WHEN** a sweep cannot read the held root or observes pending cleanup
- **THEN** reconciliation reports an error and an unseal consuming that result fails closed
