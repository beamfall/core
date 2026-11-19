## Why

CORE-2664 makes the live playback scratch root exclusive to one session manager. Directory cleanup must account for every active reader and writer, and encrypted-library unseal requires a complete scratch reconciliation.

## What Changes

- Lock the scratch root before startup reconciliation or allocation.
- Claim session directories before writing, retain failed removals for retry, and wait for owners before releasing the root.
- Keep live artifact reads and fragment joins within the claimed directory and cut off encrypted readers before waiting for release.
- Run scratch reconciliation outside the Store lock before encrypted-library publication.
