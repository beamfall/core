# Purge removes an encrypted Library's private database

Ticket: CORE-1595 (`queue:core:main`).

## Why

A deleted encrypted Library can never be removed: `purgeLibrary` refuses it and the retention sweep
skips it, because its catalog lives in a private database (ciphertext, a plaintext working copy
while unsealed, and pre-migration backups) and its keys live in the secure store, and a purge
removed neither. Such a Library stays in the trash forever.

## What Changes

- Purging a deleted encrypted Library removes its private database: the plaintext working copy and
  its WAL/SHM sidecars, the ciphertext and the pre-migration backups. It destroys the Library's key
  material in the secure store and writes the same `library.purge` activity record as any purge.
- The catalog commit is the only point of no return. Nothing is removed before it, so a failure up
  to it leaves the Library restorable. The commit records the teardown it still owes. A failed
  teardown step leaves that record, and the next retention sweep or server start finishes it, so
  the Library is never left half deleted.
- An encrypted Library is listed as purgeable and has a `purgeAfter` time. Only a Custodial Library
  stays not purgeable.

## Non-goals

- No wire change: `purgeable`, `purgeAfter` and the error vocabulary are unchanged.
- No new activity action.
