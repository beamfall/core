# feed-catalogue Specification

## Purpose
Catalogue every Subscription feed entry as a Media, download according to the Subscription's policy, and play entries that are not downloaded through proxy-tee.

## Requirements

### Requirement: Every feed entry is catalogued as a Media

A poll SHALL create one Media per feed entry with an enclosure, whether or not it is downloaded.
Entry identity SHALL be the entry GUID (else item link, else a hash of title and date), never the
enclosure URL. A not-downloaded entry's Media SHALL report availability `not-downloaded`.

#### Scenario: Back catalogue appears

- **WHEN** a feed with 40 entries is followed with policy `latest` and N = 3
- **THEN** the Library holds 40 Media for the feed, 3 are queued for download and 37 report
  `not-downloaded`

#### Scenario: Download keeps the Media

- **WHEN** the poller downloads an entry and the scan imports the file
- **THEN** the file attaches to the entry's existing Media and no second Media is created

### Requirement: Downloads follow the Subscription's policy

The poller SHALL download only entries the policy selects: `latest` (first N entries of a serial
feed, newest N otherwise), `new-only` (entries first seen after the follow), `everything`, or
none for `stream-only`. Setting `everything` SHALL require explicit confirmation. Existing
Subscriptions SHALL migrate to `new-only`.

#### Scenario: Stream-only downloads nothing

- **WHEN** a `stream-only` feed publishes a new entry
- **THEN** a Media is created and nothing is downloaded

### Requirement: A not-downloaded Media plays through proxy-tee

Playback of a not-downloaded feed Media from a public feed SHALL register a proxy-tee stream for
the enclosure and return a `media-proxy` stream URL. The enclosure URL SHALL NOT appear on the wire
or in logs.

#### Scenario: Play before download

- **WHEN** a viewer plays a `not-downloaded` entry
- **THEN** playback succeeds through `/api/media/proxy/<token>` and the Media keeps its ID

### Requirement: Feed failures are recorded and surfaced

Each poll SHALL record success or a failure class, the time failures began and the next poll time.
Failures SHALL back off exponentially with ±20% jitter, capped at 24 hours, and SHALL never disable
the Subscription. A 410 response SHALL retire it. `refreshSubscription` SHALL poll at most once per
5 minutes per Subscription.

#### Scenario: Unreachable host

- **WHEN** a feed's host does not resolve
- **THEN** the Subscription reports `lastErrorClass = dns`, a `failingSince` time and a later
  `nextPollAt`, and the web shows the cause

### Requirement: A feed Subscription publishes a show container with artwork

Polling a feed SHALL create or update one container Entity for the Subscription when the Library's
plugin declares an outermost container kind that holds Media through a single-holder relation, and
SHALL hold the feed's catalogue episodes through that relation.

#### Scenario: Show with channel art

- **WHEN** a feed with a title and an `itunes:image` is polled into such a Library
- **THEN** a container Entity titled by the feed exists with the image as its poster and each
  episode is held by it

#### Scenario: Episode artwork

- **WHEN** an episode has its own `itunes:image`
- **THEN** it is the episode's poster and thumb; an episode without one takes the show's poster

#### Scenario: Plugin without a container kind

- **WHEN** the Library's plugin declares no such kind
- **THEN** no Entity or artwork is written

### Requirement: Catalogue insertion order is newest-last

Catalogue entries SHALL be inserted in ascending publish-date order (ties keep feed order) so
the `recently-added` source, which reads newest row first, lists the newest episode first. The
entries a call returns SHALL stay in feed order.

#### Scenario: newest episode lists first
- **WHEN** a feed with five dated entries is catalogued
- **THEN** the newest entry's media row sorts ahead of the oldest in `recently-added`

### Requirement: Recently-added orders feed Media by publication time

The `recently-added` source SHALL list Media that have a subscription entry newest-published first,
using the entry's feed publish time (first-seen time when the feed gave none), regardless of the
order they were ingested. Media without a subscription entry SHALL follow them in ingest order,
newest first. Libraries holding no subscription entries SHALL keep ingest order alone.

#### Scenario: Out-of-order ingest

- **WHEN** a feed holding an older newest-episode is catalogued after a feed holding a newer one
- **THEN** `recently-added` lists the newer-published episode first

#### Scenario: Library without feeds

- **WHEN** no visible Library holds subscription entries
- **THEN** `recently-added` lists by ingest order as before
