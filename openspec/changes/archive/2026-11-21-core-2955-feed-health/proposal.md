# Subscription feed health

Ticket: CORE-2955 (`queue:core:main`). Design: ADR-0238 §2.2.

## Why

A Subscription feed that fails today is retried every hour forever with no record of why, a moved
feed keeps hitting its old URL, and a feed whose origin says it is gone keeps polling. Owners and
followers cannot see that a feed is unhealthy or ask for a retry.

## What Changes

- Every poll interval is clamped to 15 minutes to 24 hours. The poll job runs every 15 minutes and
  polls only the Subscriptions that are due.
- A failed poll records a closed error class (`dns`, `tls`, `http-4xx`, `http-5xx`, `parse`,
  `ssrf-blocked`, `auth`, `network`), when the run of failures began, and the next poll, backed off
  exponentially from 15 minutes with ±20% jitter and capped at 24 hours. A failing feed is never
  disabled; after seven days its health reads `warning`.
- A 301/308 redirect chain or an in-feed new-feed-URL declaration rewrites the feed URL after two
  consistent polls. 302, 303 and 307 are followed without a rewrite. A move to another origin is
  ignored when the feed carries a credential, and the credential is only ever sent to the original
  origin.
- A 410 Gone retires the feed: it stops polling until someone refreshes it.
- Append-only catalog migration `0129_subscription_feed_health.sql` stores the health state.
- Graph: `SubscriptionInfo` gains `healthStatus`, `lastErrorClass`, `failingSince`, `nextPollAt`,
  `retiredAt` and `movedAt`. New `refreshSubscription(id)` (any session that follows the
  Subscription, or a Manager; once per five minutes per Subscription) and Manager-only
  `refreshAllSubscriptions`.

## Out of scope

- The adaptive interval from feed `ttl` and publish cadence (CORE-2956).
- The web warning and Retry button.
