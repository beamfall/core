## ADDED Requirements

### Requirement: A Profile sees new entries from the Subscriptions it follows

The server SHALL provide a Profile-scoped Source `subscription-new` listing Media catalogued from
the Subscriptions the Profile follows whose publish time (or first-seen time when the feed gives
none) is after the Profile followed, newest first. Media the Profile has played SHALL NOT be
listed. The Source SHALL apply the caller's Library, Reveal, certification and Mute policy, and
SHALL be empty without a Profile.

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
own Library and certification policy. Entries catalogued on the Subscription's first poll SHALL NOT
be routed. A follower the queue refuses SHALL be skipped without failing the poll.

#### Scenario: Routed to the end of the queue

- **WHEN** a Profile set `queue-end` and a poll catalogues a new entry
- **THEN** the entry's Media is the last entry of that Profile's Play Queue
