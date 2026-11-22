# subscription-feed-health Specification

## Purpose
Poll Subscription feeds with back-off that never disables them, follow moved and retired feeds, and expose feed health and manual refresh on the Graph.

## Requirements

### Requirement: Subscription polls back off and never disable

The server SHALL poll a Subscription feed no more often than every 15 minutes and no less often
than every 24 hours. After a failed poll it SHALL record the failure's class, keep the time the run
of failures began, and schedule the next poll after an exponential backoff from 15 minutes with
±20% jitter, capped at 24 hours. It SHALL NOT disable a failing feed. A feed failing for seven days
or more SHALL report health `warning`. A successful poll SHALL clear the failure state.

#### Scenario: A feed fails for a month

- **WHEN** a feed's origin fails every poll for 30 days
- **THEN** the feed is still polled at least once a day and reports health `warning`

#### Scenario: Each failure class is recorded

- **WHEN** a poll fails on DNS, TLS, a 4xx or 5xx status, an unparseable document, the SSRF guard,
  a missing or refused credential, or another transport error
- **THEN** the Subscription records the matching class and never the raw error text

### Requirement: Moved and retired feeds

The server SHALL rewrite a Subscription's feed URL when two consecutive successful polls report the
same new URL, from a chain of permanent redirects or a new-feed-URL declaration in the document. It
SHALL follow temporary redirects without rewriting. It SHALL NOT send a feed credential to any
origin but the feed's own, and SHALL ignore a move to another origin for a feed with a credential.
A 410 Gone SHALL retire the feed so it is not polled until a refresh is requested.

#### Scenario: A feed moves

- **WHEN** the feed answers 301 to a new URL on two consecutive polls
- **THEN** the Subscription's feed URL becomes the new URL

#### Scenario: A feed is gone

- **WHEN** the feed answers 410
- **THEN** the Subscription reports health `retired` and is not polled again until refreshed

### Requirement: Feed health on the Graph and manual refresh

`SubscriptionInfo` SHALL carry `healthStatus`, `lastErrorClass`, `failingSince`, `nextPollAt`,
`retiredAt` and `movedAt`. `refreshSubscription(id)` SHALL make the Subscription due now for a
session that follows it or a Manager, refuse a second refresh of the same Subscription within five
minutes, and read as an unknown Subscription to any other session. `refreshAllSubscriptions` SHALL
be Manager-only.

#### Scenario: Refresh rate limit

- **WHEN** a follower refreshes a Subscription twice within five minutes
- **THEN** the first refresh makes it due and the second is refused
