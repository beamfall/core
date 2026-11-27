## ADDED Requirements

### Requirement: Trending feeds are read through the server from opted-in directories only

`trendingFeeds` SHALL return shows currently popular in a public directory, for an adult Profile or
manager device. The server SHALL query Podcast Index trending only when an operator key and secret
are configured, and Apple's top-shows chart only when `BEAMFALL_PODCAST_APPLE_CHART` is true. When
neither is enabled the server SHALL make no outbound request and SHALL return an empty list.

#### Scenario: No source enabled

- **WHEN** no Podcast Index key exists and the Apple chart is not enabled
- **THEN** `trendingFeeds` returns an empty list and no outbound request is made

#### Scenario: Podcast Index key present

- **WHEN** an operator key and secret exist
- **THEN** the results include Podcast Index trending shows labelled `directory-index`

#### Scenario: Apple chart enabled

- **WHEN** the Apple chart is enabled
- **THEN** the results include the chart's shows with their feed addresses, labelled `directory-chart`

### Requirement: Trending requests identify no install or person

Trending requests SHALL carry only the server's fixed User-Agent and the region and language
parameters. Region and language SHALL be two ASCII letters each and a malformed value SHALL fall
back to `us` and `en`.

#### Scenario: Locale-aware chart

- **WHEN** the web app asks with region `ca` and language `fr`
- **THEN** the Apple request uses the `ca` chart and the Podcast Index request uses `lang=fr`

### Requirement: Trending is cached and degrades

Results SHALL be cached per source, region and language for one hour, and a failed source SHALL be
cached as empty for five minutes. One failing source SHALL NOT remove the other's results.

#### Scenario: Repeat reads

- **WHEN** `trendingFeeds` is called twice within the hour
- **THEN** the directory is contacted once

### Requirement: Trending shows can be followed in one click

The Following page SHALL list non-empty Trending results with their source label and a Follow
control that follows the feed through `followFeed` with the selected download policy.

#### Scenario: Follow from Trending

- **WHEN** an adult selects Follow on a trending show
- **THEN** the feed is followed and the button reads Following
