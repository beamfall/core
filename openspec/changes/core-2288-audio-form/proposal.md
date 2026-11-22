# CORE-2288: Entity audio-form hint

## Why

Clients choose an audio container's detail presentation (long-form player, feed catalogue, or
album track list) from the Entity read. Today they guess from the plugin, so a client without
plugin knowledge cannot tell a followed feed's container from a long-form one, and the Apple TV
detail template (CORE-2961) has no signal at all. Ordinals are already on the wire (CORE-2386).

## What Changes

- `EntityDetailInfo` gains `audioForm` (`"feed"` or `"long-form"`, omitted otherwise) and
  `subscriptionID`, derived generically: a container Entity whose first member Media is audio
  reads `feed` when that member is a catalogued Subscription entry, else `long-form` when the
  Library's plugin resumes Media (its Engagement posture).
- Store `EntityMemberSummary` reads the first member's class and Subscription.
- Wire contract: additive minor bump after the 2.50.0 release lands.

## Impact

- Graph `entity(id)` read; no migration. Known edge: a container of resumable audio that is not
  a Subscription reads `long-form` even when its plugin labels it otherwise; clients keep their
  own label from the kind's display name.
