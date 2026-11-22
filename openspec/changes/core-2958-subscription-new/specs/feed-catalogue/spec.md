## ADDED Requirements

### Requirement: A Profile sees new entries from the Subscriptions it follows

The server SHALL provide a Profile-scoped Source `subscription-new` listing Media catalogued from
the Subscriptions the Profile follows whose publish time (or first-seen time when the feed gives
none) is after the Profile followed, newest first. Media the Profile has played SHALL NOT be
listed. The Source SHALL apply the caller's Library, Reveal, Sealed, certification and Mute
policy, and SHALL be empty without a Profile.

#### Scenario: Another Profile's follow does not leak

- **WHEN** Profile A follows a Subscription and Profile B does not
- **THEN** a new entry appears in A's `subscription-new` Source and not in B's

#### Scenario: Marking played dismisses

- **WHEN** the Profile marks a listed Media played
- **THEN** the Media leaves its `subscription-new` Source

### Requirement: New entries can be routed to the Play Queue per follow

A Profile SHALL be able to set, per followed Subscription, where new entries go: `new` (the
default), `queue-end` or `queue-front`. When a poll catalogues entries published after the follow,
each follower with a queue route SHALL get them added to its Play Queue, oldest first, under its
own Library, certification and Mute policy, minus Media it has played. A follower the queue refuses
SHALL be skipped without failing the poll.

#### Scenario: Routed to the end of the queue

- **WHEN** a Profile set `queue-end` and a poll catalogues a new entry
- **THEN** the entry's Media is the last entry of that Profile's Play Queue

### Requirement: Routing skips entries that were never new to the queue

Entries catalogued on the Subscription's first poll SHALL NOT be routed, nor entries already
catalogued when the Profile switched from `new` to a queue route.

#### Scenario: Switching route

- **WHEN** a Profile with listed new entries switches from `new` to `queue-end`
- **THEN** those entries stay in its `subscription-new` Source and are not added to its Play Queue

### Requirement: Routing adds each new entry once

Each new entry SHALL be added to a follower's Play Queue at most once. A routing pass that a crash
or a concurrent route change interrupted SHALL resume on the next poll.

#### Scenario: An interrupted pass resumes once

- **WHEN** a routing pass stops before its Play Queue write commits
- **THEN** the next poll adds the entries, and a later poll does not add them again

### Requirement: A repeated follow keeps the new-entry boundary

Following a Subscription again without unfollowing SHALL keep the original follow time.

#### Scenario: Follow twice

- **WHEN** a Profile that follows a Subscription follows it again
- **THEN** entries published between the two follows stay in its `subscription-new` Source
