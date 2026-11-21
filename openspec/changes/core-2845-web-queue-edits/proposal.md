## Why

CORE-2845 lets listeners edit the server Play Queue from music menus while idle or while listening
to a server-queue run for their current Profile.

## What Changes

- Offer Play next and Add to queue in music menus for audio Media with known unlocked state.
- Keep Play queue as the explicit start of a server-queue run. The `?playQueue=<profileID>` route
  identifies that run. Album and Play all continue to use their catalog playback paths.
- Serialize edits with server-queue advances and confirm placements with a scoped server reread.
- Use the existing queueAdd mutation with the expected Profile ID and a best-effort client lock check.

## Impact

- The first scope cut deferred catalog conversion to CORE-2865, including complete Play all and
  album reads, disc ordering, and partial-import recovery.
- The second scope cut keeps CORE-2845 web-only. CORE-2866 owns server-side queue-write enforcement
  when Reveal or certification changes after client preflight. CORE-2867 owns search-result queue
  actions and their eligibility, pagination, and refresh behavior.
