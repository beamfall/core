# Feed catalogue, download policy, play-without-download and feed health

Tickets: CORE-2953 (download policy and file-less catalogue), CORE-2954 (play a not-downloaded
Media through proxy-tee), CORE-2955 (feed health), CORE-2957 (web follow flow and feed home), all
`queue:core:main`. Decided in ADR-0238; the feed strategy is `core-private/docs/design/podcasts.md`
§3.0.

## Why

A Subscription today downloads every enclosure it sees and nothing else exists in the catalogue
until a file lands. A viewer cannot see a feed's back catalogue, cannot play an entry without
downloading it, and a broken feed only writes a log line. ADR-0238 decides the fix.

## What Changes

- Migration `0130_subscription_catalogue_health.sql` adds the download policy, latest-N and health
  columns to `subscription` and a `subscription_entry` table: one row per feed entry, bound to a
  file-less Media.
- Every feed entry becomes a Media with no file (availability `not-downloaded`), ID
  `ProvisionalMediaID(library, subscriptionID + "\x00" + guid)`. The poller downloads only what the
  policy asks for: `latest` (first N for serial feeds, newest N otherwise; default 3), `new-only`
  (entries first seen after the follow), `everything` (needs confirmation), `stream-only`.
  Existing Subscriptions become `new-only`. A policy download binds to the entry's Media instead of
  forking a fingerprint Media.
- Playing a not-downloaded Media registers a proxy-tee stream for its enclosure and returns a
  `media-proxy` stream URL. The enclosure URL is stored like `feed_url` (secret class: never on the
  wire, never logged). Private feeds (with an auth header) are not streamed in this change.
- Feed health: each poll records success or a failure class (`dns`, `tls`, `http-4xx`, `http-5xx`,
  `parse`, `ssrf-blocked`, `auth`, `gone`), `failingSince` and `nextPollAt`. Failures back off
  (doubling from the base interval, ±20% jitter, cap 24 h). Feeds are never auto-disabled; a 410
  retires the feed. `refreshSubscription` polls now, at most once per 5 minutes.
- Graph: `SubscriptionInfo` gains `downloadPolicy`, `downloadLatestN`, `health`, `entryCount`,
  `downloadedCount`, `catalogBytes`; new `followFeed`, `refreshSubscription`,
  `setSubscriptionDownloadPolicy` mutations and a `subscriptionEntries` read.
- Web: follow by search or URL, a followed-feed list with health, and a feed's catalogue with play.

## Out of scope

Adaptive polling (CORE-2956), feed moves after two observations, retention (CORE-2968), Apple TV
(CORE-2961), streaming private feeds.
