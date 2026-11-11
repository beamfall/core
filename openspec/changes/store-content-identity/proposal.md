# Repair managed file identity, JSON validation and store error semantics

Ticket: CORE-0698 (`queue:core:main`).

## Why

A release review found these defects in the store:

- Managed import and managed convert fingerprint the file with `HashMediaFilePath`, a placeholder
  that hashes the path. Two copies of the same bytes get different identities, and new bytes written
  at an old path keep the old identity.
- Job payloads, checkpoints, job events and outbox payloads are stored without checking that they
  are JSON.
- Saved filter and smart collection reads report every database failure as not-found, and the
  restricted-library annotation for source items treats a failed lookup as "not restricted".

## What Changes

- Managed import hashes the whole file while copying it into staging; managed convert hashes the
  staged artifact before admitting it. Both store that `sha256:` content fingerprint. The path-hash
  placeholder is no longer used by managed import or convert.
  Because `media_file.content_fingerprint` is unique, a managed admission whose fingerprint is
  already live at another path or in another library is refused with a `ValidationError` instead of
  repointing the existing row.
- Job payloads, checkpoints, job event payloads and outbox payloads must be valid JSON. Invalid input
  is a `ValidationError` and nothing is written.
- Saved filter and smart collection reads return not-found only for `sql.ErrNoRows` and pass other
  errors through. A failed restricted-library lookup fails the read instead of marking the item
  unrestricted.

## Non-goals

- Crash-safe journaling of batch file moves (`OrganizeApply`, `BatchRenameManagedPaths`) is
  deferred to its own change. Batch moves keep their current in-transaction move and compensation,
  and store open does not replay anything.
- Refusing to mark a populated plaintext library encrypted is deferred: it changes fixtures outside
  the store package that encrypt a seeded library.
- No schema migration.

## Failure modes

- A managed import or convert that cannot read or hash its staged file fails before admission and
  removes its staging.
- An existing database whose fingerprints came from the path placeholder keeps them; only new
  managed imports and converts use content fingerprints.

## Acceptance evidence

- `internal/store` tests: a second import of equal bytes at another path is refused and the first
  media file keeps its path, library and Media; changed bytes at a reused path get a new identity; a
  converted artifact carries its content fingerprint.
- Malformed JSON in each job and outbox entry point is rejected and not persisted.
- With the table removed underneath it, a saved filter or smart collection read returns the database
  error, not not-found; a failing restricted-library lookup returns its error.

## Rollback

Revert the change. Content fingerprints written in the meantime remain valid fingerprints.

## Capabilities

### New Capabilities

- `store-integrity`: content identity of managed files, JSON validation and not-found semantics in
  the store.

### Modified Capabilities

None.
