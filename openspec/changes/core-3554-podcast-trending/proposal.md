# Podcasts: Trending shows

Ticket: CORE-3554 (`queue:core:main`). Builds the Trending half of CORE-2966 (design
`podcasts.md` §3.5) and, by owner decision 2026-11-26, adds Apple's top-shows chart behind an
operator opt-in. CORE-3003 (chart terms unpublished) stays open: the chart is never on by default.

## Why

A household can follow a feed only by pasting its address or by searching a name it already knows.
There is no way to see what is popular and follow it in a click.

## What Changes

- **Wire**: new read field `trendingFeeds(region: String, language: String)` returning
  `[FeedTrendingResult]` (`title feedURL artworkURL description source`). `source` is the closed
  vocabulary `podcast-index|apple-podcasts` so clients label provenance. Adult Profile or manager
  device, as `searchFeeds`. Additive wire-contract minor bump (3.3.0).
- **Sources, both off until configured**:
  - Podcast Index `/podcasts/trending`, only when the operator key and secret exist in the secure
    store (the keys `searchFeeds` already reads).
  - Apple's top-shows chart (`rss.marketingtools.apple.com`, resolved to feed URLs through the
    iTunes lookup endpoint), only when `BEAMFALL_PODCAST_APPLE_CHART` is true. Default false.
  - With neither enabled the server makes no outbound request and returns an empty list.
- **Privacy**: requests carry the server's fixed User-Agent and the region and language only: no
  install, Profile, library or listening data. Region and language come from the viewer's locale
  (`en`, `fr-CA`) and are validated as two letters each.
- **Cost bounds**: results cached per source, region and language for 1 hour; a failed fetch is
  cached for 5 minutes so an outage is not retried per request. Source failures degrade to the
  other source; if all fail the field returns an empty list.
- **Web**: the Following page lists Trending (when non-empty) above the feed list, each with its
  provenance label and a Follow button using the existing `followFeed` mutation and the chosen
  download policy. EN and fr-CA strings.

## Out of scope

Manager UI toggle for the Apple chart, categories, "because you follow" similarity, Apple TV and
mobile surfaces, the release-build PI key (CORE-3005).
