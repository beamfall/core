## Why

CORE-2845 lets listeners edit the server Play Queue from music menus while idle or while listening
to a server-queue run for their current Profile.

## What Changes

- Offer Play next and Add to queue for audio Media with known unlocked state.
- Keep Play queue as the explicit start of a server-queue run. The `?playQueue=<profileID>` route
  identifies that run. Album and Play all continue to use their catalog playback paths.
- Serialize edits with server-queue advances and confirm placements with a scoped server reread.
- Add an optional Reveal precondition to queueAdd so a relock can refuse the write at commit time.

## Impact

The web viewer uses existing Play Queue reads and supplies the new precondition on queueAdd.
Existing clients may omit it. Catalog conversion, including
complete Play all and album reads, disc ordering, and partial-import recovery, is follow-up work.
