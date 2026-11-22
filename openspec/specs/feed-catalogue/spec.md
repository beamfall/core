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
