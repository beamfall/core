# Recover first-run setup after a partial owner/device write

Ticket: CORE-0757 (`queue:core:main`).

## Why

First-run setup (`pairing.Service.Bootstrap`) writes the owner Profile, the owner designation and
the first Manager Device with its Session in three separate store transactions. When enrollment
fails or the process stops after the Profile commits, the adult Profile stays durable. On the next
start `bootstrapSetupToken` sees an adult Profile, treats setup as complete and installs no setup
token, so the server can no longer be set up through `/web/setup` without editing the database.

## What Changes

- The store gains one first-run write, `Store.BootstrapOwner`, that commits the owner Profile, its
  owner designation, the first Manager Device and its Session in a single catalog transaction. A
  failure at any step leaves none of them behind. `Bootstrap` uses it in place of the three
  separate writes.
- First-run setup counts as complete only when an adult Profile exists and at least one Device has
  been enrolled (`Store.FirstRunSetupComplete`). Devices are revoked by flag, never deleted, so a
  server that completed setup never qualifies for a new setup token. `bootstrapSetupToken` uses it
  in place of "an adult Profile exists".
- A store left by the earlier non-atomic write (adult Profile, no Device) is a recovery state:
  startup installs a fresh setup token, and the next successful `Bootstrap` reuses the existing
  owner Profile (or, when none is designated, the oldest adult Profile) instead of creating a
  second one. It designates that Profile owner, renames it to the requested name, and enrolls the
  first Manager Device bound to it.

## Non-goals

- Changing the setup token's format, its single-use rule within a process, the setup throttle, or
  how the token is shown to the operator.
- Making the admin password write part of the same transaction. It is an idempotent overwrite
  gated by the same token; a retry rewrites it.
- Removing stray adult Profiles left by repeated failed attempts before this change.
- A schema change. No migration is added.

## Failure modes

- Enrollment fails inside `BootstrapOwner`: the transaction rolls back, the token stays installed
  for an in-process retry, and a restart reissues a token because no Device exists.
- A restart after success: an adult Profile and a Device exist, so no token is installed and
  `/web/setup` stays refused.

## Acceptance evidence

- `internal/app/secrets_test.go` `TestAuditBootstrapRestartsAfterPartialOwnerWrite`: a store with
  the legacy partial write is reopened as a new process, receives a setup token, completes setup
  through `Bootstrap` with one owner Profile and one Device, and receives no token on the next
  reopen.
- `internal/store/store_test.go`: a fault-injected enrollment failure inside `BootstrapOwner`
  leaves no Profile, owner or Device; a reopened store completes setup; completion state is
  reported correctly before and after.

## Rollback

Revert the change. No stored state depends on it.

## Capabilities

### New Capabilities

- `first-run-setup`: when the server issues a first-run setup token and what a successful setup
  commits.

### Modified Capabilities

None.
