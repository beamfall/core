## Why

CORE-2766. Track-level credit strings fill the music Library's Artists view with entries that only represent a credit on a track within an album. A credit string can also be a single artist's name, so it must stay intact.

## What Changes

- Let a compiled plugin hook declare relation-based eligibility for an Entity kind in Library browse.
- List album artists and artists credited on tracks without an album in the music Library's Artists view.
- Preserve the underlying Entities and credits for detail, search, track rows and merges.
