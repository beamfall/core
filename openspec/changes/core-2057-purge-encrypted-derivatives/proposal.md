# Purging an encrypted Library removes its encrypted derivatives

Ticket: CORE-2057 (`queue:core:main`).

## Why

Purging a deleted encrypted Library removes its private database and erases its keys, but the
Library's encrypted derivative caches stay on disk. These are the precomputed renditions under
`.precompute-encrypted/<library>/` and the Library's encrypted subtitle cache. With the keys gone
they cannot be read, but they still take space and they still record that the Library existed.

## What Changes

- The store takes the same derivative purgers the DEK rewrap already uses: precomputed renditions
  and the encrypted subtitle cache.
- A purge runs every purger for the Library after the catalog commit and the private-database
  removal, with no lock held.
- If a purger fails, the purge's pending teardown record is kept and the keys are still erased.
  The next retention sweep or server start runs the purger again.

## Non-goals

- Generated-asset ciphertext is not removed. It is stored per content fingerprint, a fingerprint
  can be shared with other Libraries, and it cannot be attributed while the Library is Sealed.
- No change to soft trash. Trashing never removes derivatives, so a trashed Library stays
  restorable.
- No individual encrypted Media hard delete. The store has no such path today: media trash and
  media purge act only on the shared catalog.
