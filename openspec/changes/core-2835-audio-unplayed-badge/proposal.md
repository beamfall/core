# Drop the remaining count from audio containers

Ticket: CORE-2835

## Why

Album cards showed a loud accent pill such as "50 unplayed" over the artwork. Nobody works
through an album as a queue to finish, so on an untouched album the count only repeats the
track count, and the accent fill fought the cover art.

## What Changes

- Audio containers (`mediaClass` audio) no longer show the remaining count on their card or in
  the Entity page metadata line. Video and photo containers keep it.
- The artwork count badge becomes a quiet translucent pill that matches the finished mark,
  instead of an accent fill.

## Non-goals

- No change to playback state on the wire or to the "Up next" label on audio Entity pages.
