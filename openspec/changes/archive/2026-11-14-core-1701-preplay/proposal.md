# Pre-play queue (cinema mode)

Ticket: CORE-1701 (`queue:core:main`).
Paired change: beamfall-plugin-filmtv `openspec/changes/core-1701-preplay/` (the film policy).

## Why

Owners coming from other media servers expect "cinema mode": short intro clips of their choosing
and a few trailers before a film, each skippable. Beamfall plays the film straight away. Core has
no seam a plugin can use to put items ahead of a feature, and no place for the Owner's intro
clips.

## What Changes

- **Owner clip list**: a server-wide, ordered list of up to 10 Media IDs (`preplay.clips` in the
  server configuration). New admin read `prePlayClips` and admin mutation
  `setPrePlayClips(mediaIDs)`; every ID must name an existing, untrashed Media.
- **Per-Profile settings**: two namespaced client preferences, `preplay.enabled` (`on` enables)
  and `preplay.extras` (an integer 0..10, default 2). They hold no content references. The web
  app's playback settings edit them.
- **Pre-play seam**: `graph.PrePlayPolicy`, contributed by an active plugin through
  `app.Hooks.PrePlayPolicy`. Core asks the policy whether a feature gets pre-play and which
  suggested items to play; Core never decides that a Media is a film. With no active policy, no
  feature gets pre-play.
- **Store read for policies**: `MediaExtraLinks(featureID, filter)` lists the Extra-of relations
  in the feature's Library (Extra, parent, path), bounded and filtered by trash and certification.
- **Session read `prePlayQueue(mediaID)`**: when the Profile has enabled pre-play and the policy
  says the feature is eligible, returns the Owner's clips then the policy's suggested items, each
  with id, title, duration, role (`clip` or `suggested`) and label. Every item is re-checked
  against the caller's Library visibility. Anything else returns an empty list.
- **Web player**: before a feature starts, the player plays the pre-play queue in order with a
  "Skip" (this item) and "Skip all" control. Pre-play items record no watch progress.

## Not in this change

- Apple and Android client playback of the queue (follow-up tickets).
- Trailers fetched from outside the Library (CORE-0363 owns trailers absent from the catalog).
- Per-Library clip lists.

## Failure modes

- Policy error, missing policy, hidden feature or disabled Profile: empty queue; the feature plays
  normally.
- A configured clip that was trashed or is hidden from the Profile: dropped from that Profile's
  queue.
- Corrupt stored clip list: read as empty.

## Acceptance evidence

- Store tests: clip list round-trip, de-duplication, cap, unknown ID, corrupt value;
  `MediaExtraLinks` bounds, trash and visibility.
- Graph tests: disabled Profile, no policy, ineligible feature, ordering and visibility filtering,
  admin-only clip mutation.
- Web unit test of the pre-play sequence and skip controls.

## Rollback

Revert the change. The `preplay.*` keys are inert without the code; no migration is added.
